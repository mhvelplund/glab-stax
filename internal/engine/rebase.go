// Package engine implements multi-branch operations over a stack.
package engine

import (
	"fmt"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// RebaseOptions selects which layers to rebase and how.
type RebaseOptions struct {
	Anchor        string // branch that --downstack/--upstack are relative to
	Downstack     bool   // trunk up to and including Anchor
	Upstack       bool   // Anchor up to the top
	NoTrunk       bool   // skip the fetch and leave the bottom layer where it is
	Remote        string // defaults to the stack's remote
	PreserveDates bool   // --committer-date-is-author-date
	// AbortOnConflict restores every branch instead of pausing on a conflict.
	AbortOnConflict bool
	// TrunkSHA, when set, is the commit to rebase the bottom layer onto: the
	// caller has already fetched and fast-forwarded (FastForwardFromRemote).
	TrunkSHA string
}

// rebaseState is the journal for a paused cascade rebase.
type rebaseState struct {
	StackKey        string            `json:"stackKey"` // name of the stack's first branch
	OriginalBranch  string            `json:"originalBranch"`
	OriginalDir     string            `json:"originalDir"`
	RestoreRefs     map[string]string `json:"restoreRefs"`  // branch tips before anything moved, for --abort
	OriginalRefs    map[string]string `json:"originalRefs"` // tips after fast-forwarding, before rebasing
	TrunkSHA        string            `json:"trunkSha"`
	Order           []string          `json:"order"` // layers to rebase, bottom first
	Next            int               `json:"next"`  // index in Order of the layer in progress
	ConflictDir     string            `json:"conflictDir,omitempty"`
	PreserveDates   bool              `json:"preserveDates,omitempty"`
	AbortOnConflict bool              `json:"abortOnConflict,omitempty"`
}

// Rebaser runs cascade rebases. Repo is the worktree the command was run from.
type Rebaser struct {
	Repo  *git.Repo
	Store stack.Store
	Log   func(format string, args ...any)
}

// InProgress reports whether a cascade rebase is paused.
func (r *Rebaser) InProgress() bool { return r.Store.HasJournal(stack.RebaseJournal) }

// Start rebases the selected layers of s, bottom up, each onto the layer below it.
// On success it updates the recorded bases in s; the caller saves the file.
func (r *Rebaser) Start(s *stack.Stack, o RebaseOptions) error {
	if r.InProgress() {
		return exitcode.New(exitcode.RebaseInProgress, "a stack rebase is already in progress; run `glab stax rebase --continue` or `--abort`")
	}
	order := selectRange(s, o)
	if len(order) == 0 {
		r.Log("No branches to rebase")
		return nil
	}
	cur, err := r.Repo.CurrentBranch()
	if err != nil {
		return err
	}
	top, err := r.Repo.TopLevel()
	if err != nil {
		return err
	}
	if err := r.preflight(order); err != nil {
		return err
	}

	st := &rebaseState{
		StackKey: s.Branches[0].Name, OriginalBranch: cur, OriginalDir: top,
		RestoreRefs: r.tips(s), Order: order, PreserveDates: o.PreserveDates,
		AbortOnConflict: o.AbortOnConflict,
	}
	switch {
	case o.TrunkSHA != "":
		st.TrunkSHA = o.TrunkSHA
	case o.NoTrunk:
		if st.TrunkSHA, err = r.Repo.RevParse(git.Ref(s.Trunk)); err != nil {
			return err
		}
	default:
		remote := o.Remote
		if remote == "" {
			remote = s.Remote
		}
		if st.TrunkSHA, err = r.UpdateFromRemote(s, remote); err != nil {
			return err
		}
	}
	st.OriginalRefs = r.tips(s)
	if err := r.Store.SaveJournal(stack.RebaseJournal, st); err != nil {
		return err
	}
	r.Log("Rebasing %s", strings.Join(order, " → "))
	return r.run(s, st, 0)
}

// Continue resumes a paused rebase after the user resolved conflicts.
func (r *Rebaser) Continue(f *stack.File) error {
	st, s, err := r.load(f)
	if err != nil {
		return err
	}
	branch := st.Order[st.Next]
	wt := r.Repo.At(st.ConflictDir)
	if in, _ := wt.RebaseInProgress(); in {
		if files, _ := wt.ConflictedFiles(); len(files) > 0 {
			r.reportConflict(wt, branch, files)
			return exitcode.New(exitcode.RebaseConflict, "resolve the conflicts and `git add` the files first")
		}
		if _, err := wt.RunEnv([]string{"GIT_EDITOR=true"}, "rebase", "--continue"); err != nil {
			return r.handleFailure(st, wt, err)
		}
	} else if newBase, _ := r.newBase(s, st, branch); !r.Repo.IsAncestor(newBase, git.Ref(branch)) {
		// No rebase is paused, yet the layer is not on its new base: its rebase
		// never started or was aborted with git. Run it again.
		r.Log("%s was not rebased yet; rebasing it again", branch)
		return r.run(s, st, st.Next)
	}
	r.Log("Rebased %s", branch)
	return r.run(s, st, st.Next+1)
}

// Abort restores every layer to where it was before the rebase started.
func (r *Rebaser) Abort() error {
	var st rebaseState
	ok, err := r.Store.LoadJournal(stack.RebaseJournal, &st)
	if err != nil {
		return err
	}
	if !ok {
		return exitcode.New(exitcode.Generic, "no stack rebase in progress")
	}
	if st.ConflictDir != "" {
		wt := r.Repo.At(st.ConflictDir)
		if in, _ := wt.RebaseInProgress(); in {
			if _, err := wt.Run("rebase", "--abort"); err != nil {
				return err
			}
		}
	}
	for branch, sha := range st.RestoreRefs {
		if err := r.resetBranch(branch, sha); err != nil {
			return err
		}
	}
	r.restoreCheckout(&st)
	if !st.AbortOnConflict {
		r.Log("Rebase aborted; all branches restored")
	}
	return r.Store.ClearJournal(stack.RebaseJournal)
}

func (r *Rebaser) load(f *stack.File) (*rebaseState, *stack.Stack, error) {
	var st rebaseState
	ok, err := r.Store.LoadJournal(stack.RebaseJournal, &st)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, exitcode.New(exitcode.Generic, "no stack rebase in progress")
	}
	s := f.Find(st.StackKey)
	if s == nil {
		return nil, nil, exitcode.New(exitcode.NotInStack, "the stack being rebased no longer exists; run `glab stax rebase --abort`")
	}
	return &st, s, nil
}

// selectRange returns the unmerged layers to rebase, bottom first.
func selectRange(s *stack.Stack, o RebaseOptions) []string {
	start, end := 0, len(s.Branches)
	anchor := s.Index(o.Anchor)
	if anchor < 0 {
		anchor = 0
	}
	if o.Downstack {
		end = anchor + 1
	}
	if o.Upstack {
		start = anchor
	}
	if o.NoTrunk {
		// The bottom unmerged layer stays where it is.
		bottom := 0
		for bottom < len(s.Branches) && s.Branches[bottom].Merged {
			bottom++
		}
		start = max(start, bottom+1)
	}
	var order []string
	for i := start; i < end; i++ {
		if !s.Branches[i].Merged {
			order = append(order, s.Branches[i].Name)
		}
	}
	return order
}

// Preflight checks that the worktrees a rebase of order would touch are clean and idle.
func (r *Rebaser) Preflight(order []string) error { return r.preflight(order) }

// preflight checks that every worktree we will touch is clean and idle.
func (r *Rebaser) preflight(order []string) error {
	top, err := r.Repo.TopLevel()
	if err != nil {
		return err
	}
	dirs := map[string]bool{top: true}
	for _, b := range order {
		owner, err := r.Repo.WorktreeFor(b)
		if err != nil {
			return err
		}
		if owner != "" {
			dirs[owner] = true
		}
	}
	for dir := range dirs {
		wt := r.Repo.At(dir)
		if in, _ := wt.RebaseInProgress(); in {
			return exitcode.New(exitcode.RebaseInProgress, "a git rebase is in progress in %s", dir)
		}
		clean, err := wt.IsClean()
		if err != nil {
			return err
		}
		if !clean {
			return exitcode.New(exitcode.Generic, "worktree %s has uncommitted changes; commit or stash them first", dir)
		}
	}
	return nil
}

// tips returns the current SHA of every local branch in s.
func (r *Rebaser) tips(s *stack.Stack) map[string]string {
	refs := map[string]string{}
	for _, b := range s.Branches {
		if sha, err := r.Repo.RevParse(git.Ref(b.Name)); err == nil {
			refs[b.Name] = sha
		}
	}
	return refs
}

// UpdateFromRemote fetches, fast-forwards the trunk and any layer that is
// behind its remote branch, and returns the SHA to rebase the bottom layer onto.
func (r *Rebaser) UpdateFromRemote(s *stack.Stack, remote string) (string, error) {
	if err := r.Repo.Fetch(remote); err != nil {
		return "", exitcode.Wrap(exitcode.Generic, fmt.Errorf("fetch %s: %w", remote, err))
	}
	return r.FastForwardFromRemote(s, remote)
}

// FastForwardFromRemote is UpdateFromRemote for a caller that has just fetched.
func (r *Rebaser) FastForwardFromRemote(s *stack.Stack, remote string) (string, error) {
	trunkSHA, err := r.Repo.RevParse("refs/remotes/" + remote + "/" + s.Trunk)
	if err != nil {
		// The trunk only exists locally.
		return r.Repo.RevParse(git.Ref(s.Trunk))
	}
	r.Log("Fetched latest %s from %s", s.Trunk, remote)
	if err := r.fastForward(s.Trunk, trunkSHA); err != nil {
		r.Log("Warning: could not fast-forward local %s: %v", s.Trunk, err)
	}
	for _, b := range s.Active() {
		remoteSHA, err := r.Repo.RevParse("refs/remotes/" + remote + "/" + b.Name)
		if err != nil {
			continue
		}
		// Only commits from elsewhere are taken in: a remote tip the branch had
		// before was left behind on purpose (e.g. a pushed commit dropped with
		// `git reset`).
		if r.Repo.Includes(b.Name, remoteSHA) {
			continue
		}
		if err := r.fastForward(b.Name, remoteSHA); err != nil {
			return "", err
		}
	}
	return trunkSHA, nil
}

// fastForward moves branch to target if that is a fast-forward; otherwise it does nothing.
func (r *Rebaser) fastForward(branch, target string) error {
	local, err := r.Repo.RevParse(git.Ref(branch))
	if err != nil || local == target || !r.Repo.IsAncestor(local, target) {
		return nil
	}
	owner, err := r.Repo.WorktreeFor(branch)
	if err != nil {
		return err
	}
	if owner != "" {
		_, err = r.Repo.At(owner).Run("merge", "--ff-only", "--quiet", target)
	} else {
		_, err = r.Repo.Run("update-ref", git.Ref(branch), target, local)
	}
	if err == nil {
		r.Log("Fast-forwarded %s", branch)
	}
	return err
}

// run rebases st.Order[from:], then finishes. A layer checked out in some
// worktree is rebased there; any other layer is checked out and rebased in
// the worktree the rebase started from, whose checkout finish restores.
func (r *Rebaser) run(s *stack.Stack, st *rebaseState, from int) error {
	home := r.Repo.At(st.OriginalDir)
	for i := from; i < len(st.Order); i++ {
		branch := st.Order[i]
		display, args, err := r.plan(s, st, branch)
		if err != nil {
			_ = r.Abort()
			return err
		}
		owner, err := r.Repo.WorktreeFor(branch)
		if err != nil {
			return err
		}
		wt := home
		if owner != "" {
			wt = r.Repo.At(owner)
		} else {
			args = append(args, branch) // checks the branch out first
		}
		st.Next, st.ConflictDir = i, wt.Dir
		if err := r.Store.SaveJournal(stack.RebaseJournal, st); err != nil {
			return err
		}
		if _, err := wt.Run(args...); err != nil {
			return r.handleFailure(st, wt, err)
		}
		r.Log("Rebased %s onto %s", branch, display)
	}
	return r.finish(s, st)
}

// newBase returns what branch goes onto, as a revision and as a name to show:
// the nearest unmerged layer below, else the trunk commit.
func (r *Rebaser) newBase(s *stack.Stack, st *rebaseState, branch string) (rev, display string) {
	if parent := s.Parent(branch); parent != s.Trunk {
		return git.Ref(parent), parent
	}
	return st.TrunkSHA, short(st.TrunkSHA)
}

// plan returns the rebase command for branch: the bottom layer goes onto the
// trunk, others go --onto the nearest unmerged layer below, cutting at the
// previous tip of the branch directly below.
func (r *Rebaser) plan(s *stack.Stack, st *rebaseState, branch string) (string, []string, error) {
	idx := s.Index(branch)
	newBase, display := r.newBase(s, st, branch)
	ref := git.Ref(branch)
	args := []string{"rebase", "--quiet"}
	if st.PreserveDates {
		args = append(args, "--committer-date-is-author-date")
	}
	if idx == 0 {
		// A plain rebase onto the trunk, which also drops commits the trunk has
		// already. But if the recorded base holds commits the trunk lacks (the
		// layer sat on squash-merged layers that were pruned from the stack),
		// cut there so those commits are not replayed.
		if b := s.Branches[0].Base; b != "" && r.Repo.IsAncestor(b, ref) && !r.Repo.IsAncestor(b, newBase) {
			return display, append(args, "--onto", newBase, b), nil
		}
		return display, append(args, newBase), nil
	}
	below := s.Branches[idx-1].Name
	candidates := []string{st.OriginalRefs[below], s.Branches[idx].Base}
	forkRef := newBase
	if newBase == st.TrunkSHA {
		forkRef = git.Ref(s.Trunk) // --fork-point needs a ref with a reflog, not a SHA
	}
	if fp, err := r.Repo.ForkPoint(forkRef, ref); err == nil {
		candidates = append(candidates, fp)
	}
	// Cut at the latest candidate the branch contains: the parent's tip as
	// last restacked if the parent dropped commits since (they must not come
	// back here), its current tip if the branch was restacked onto it by hand.
	cut := ""
	for _, c := range candidates {
		if c != "" && r.Repo.IsAncestor(c, ref) && (cut == "" || r.Repo.IsAncestor(cut, c)) {
			cut = c
		}
	}
	if cut == "" {
		return "", nil, fmt.Errorf("could not determine where %s starts after %s changed; rebase it manually", branch, below)
	}
	return display, append(args, "--onto", newBase, cut), nil
}

func (r *Rebaser) handleFailure(st *rebaseState, wt *git.Repo, err error) error {
	branch := st.Order[st.Next]
	in, _ := wt.RebaseInProgress()
	if st.AbortOnConflict {
		if aerr := r.Abort(); aerr != nil {
			return fmt.Errorf("rebasing %s failed (%v), and restoring branches failed: %w", branch, err, aerr)
		}
		if !in {
			return fmt.Errorf("rebase of %s failed, so all branches were restored: %w", branch, err)
		}
		return exitcode.New(exitcode.RebaseConflict, "conflict while rebasing %s; all branches were restored. Run `glab stax rebase` to resolve it", branch)
	}
	if !in {
		return fmt.Errorf("rebase of %s failed: %w; fix the problem, then run `glab stax rebase --continue` (or `--abort`)", branch, err)
	}
	files, _ := wt.ConflictedFiles()
	r.reportConflict(wt, branch, files)
	if len(files) == 0 {
		// git stopped for another reason, e.g. untracked files in the way.
		return exitcode.Wrap(exitcode.RebaseConflict, fmt.Errorf("rebasing %s stopped: %w", branch, err))
	}
	return exitcode.New(exitcode.RebaseConflict, "conflict while rebasing %s", branch)
}

func (r *Rebaser) reportConflict(wt *git.Repo, branch string, files []string) {
	if len(files) == 0 {
		r.Log("Rebasing %s stopped in %s without conflicts; see the error below.", branch, wt.Dir)
	} else {
		r.Log("Conflict while rebasing %s in %s:", branch, wt.Dir)
	}
	for _, f := range files {
		lines := wt.ConflictLines(f)
		if len(lines) == 0 {
			r.Log("  %s", f)
			continue
		}
		nums := make([]string, len(lines))
		for i, n := range lines {
			nums[i] = fmt.Sprint(n)
		}
		r.Log("  %s (lines %s)", f, strings.Join(nums, ", "))
	}
	r.Log("Resolve the conflicts, `git add` the files, then run `glab stax rebase --continue`.")
	r.Log("Or undo everything with `glab stax rebase --abort`.")
}

// finish records the new bases, restores the original checkout and clears the journal.
func (r *Rebaser) finish(s *stack.Stack, st *rebaseState) error {
	for _, name := range st.Order {
		parent := s.Parent(name)
		base := st.TrunkSHA
		if parent != s.Trunk {
			sha, err := r.Repo.RevParse(git.Ref(parent))
			if err != nil {
				return err
			}
			base = sha
		}
		s.Get(name).Base = base
	}
	r.restoreCheckout(st)
	r.Log("Rebased %d branch(es). Run `glab stax push` to publish them.", len(st.Order))
	return r.Store.ClearJournal(stack.RebaseJournal)
}

// restoreCheckout checks the original branch out again in the original
// worktree. Failing to is only a warning: the branches themselves are done.
func (r *Rebaser) restoreCheckout(st *rebaseState) {
	if st.OriginalBranch == "" {
		return
	}
	wt := r.Repo.At(st.OriginalDir)
	cur, err := wt.CurrentBranch()
	if err == nil && cur == st.OriginalBranch {
		return
	}
	if err == nil {
		err = wt.Checkout(st.OriginalBranch)
	}
	if err != nil {
		r.Log("Warning: could not check out %s again in %s: %v", st.OriginalBranch, st.OriginalDir, err)
	}
}

// resetBranch moves branch to sha. A branch checked out in a worktree is reset
// there with --keep, which refuses rather than discard uncommitted changes.
func (r *Rebaser) resetBranch(branch, sha string) error {
	cur, err := r.Repo.RevParse(git.Ref(branch))
	if err == nil && cur == sha {
		return nil
	}
	owner, err := r.Repo.WorktreeFor(branch)
	if err != nil {
		return err
	}
	if owner != "" {
		if _, err := r.Repo.At(owner).Run("reset", "--keep", "--quiet", sha); err != nil {
			return fmt.Errorf("restoring %s in %s: %w (commit or stash the changes there, then run `glab stax rebase --abort` again)", branch, owner, err)
		}
		return nil
	}
	return r.Repo.SetBranchSHA(branch, sha)
}

func short(ref string) string {
	if len(ref) == 40 {
		return ref[:7]
	}
	return ref
}
