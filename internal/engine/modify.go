package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// Layer is one layer of the stack after a modify: Name holds the commits of
// the original layers listed in Sources, in order. Orig is the layer's
// original name ("" for an inserted layer).
type Layer struct {
	Name    string   `json:"name"`
	Orig    string   `json:"orig,omitempty"`
	Sources []string `json:"sources,omitempty"`
}

// commitRange is an original layer's own commits: Base..Tip.
type commitRange struct {
	Base string `json:"base"`
	Tip  string `json:"tip"`
}

// modifyState is the journal for a paused modify.
type modifyState struct {
	StackKey       string                 `json:"stackKey"`
	OriginalBranch string                 `json:"originalBranch"`
	OriginalDir    string                 `json:"originalDir"` // the worktree the layers are built in
	Layers         []Layer                `json:"layers"`      // bottom first
	Ranges         map[string]commitRange `json:"ranges"`
	Start          string                 `json:"start"`   // where the bottom layer starts
	NewTips        map[string]string      `json:"newTips"` // by layer name, as layers are built
	Next           int                    `json:"next"`    // layer being built
	// PickStart is the commit layer Next's cherry-picks started from, so a
	// resumed modify can tell whether they landed.
	PickStart string `json:"pickStart,omitempty"`
}

// Modifier applies a restructured layer list to a stack.
type Modifier struct {
	Repo  *git.Repo
	Store stack.Store
	Log   func(format string, args ...any)
}

// at returns a Modifier that works in worktree dir.
func (m *Modifier) at(dir string) *Modifier {
	return &Modifier{Repo: m.Repo.At(dir), Store: m.Store, Log: m.Log}
}

// Ranges returns each active layer's own commits, keyed by branch.
func (m *Modifier) Ranges(s *stack.Stack) (map[string]commitRange, error) {
	out := map[string]commitRange{}
	for _, b := range s.Active() {
		tip, err := m.Repo.RevParse(git.Ref(b.Name))
		if err != nil {
			return nil, err
		}
		base := b.Base
		mb, mbErr := m.Repo.MergeBase(git.Ref(s.Parent(b.Name)), tip)
		switch {
		case base == "" || !m.Repo.IsAncestor(base, tip):
			if mbErr != nil {
				return nil, mbErr
			}
			base = mb
		case mbErr == nil && base != mb && m.Repo.IsAncestor(base, mb):
			// The layer was restacked by hand onto newer parent commits since
			// its base was recorded; those commits are not its own.
			base = mb
		}
		out[b.Name] = commitRange{Base: base, Tip: tip}
	}
	return out, nil
}

// CheckLinear fails if any layer contains merge commits.
func (m *Modifier) CheckLinear(ranges map[string]commitRange) error {
	for name, r := range ranges {
		out, err := m.Repo.Run("rev-list", "--merges", r.Base+".."+r.Tip)
		if err != nil {
			return err
		}
		if out != "" {
			return exitcode.New(exitcode.Generic, "%s contains merge commits; modify needs linear history", name)
		}
	}
	return nil
}

// Apply rebuilds the stack as layers (bottom first). On success it updates s
// and returns nil; on a conflict it pauses with a journal (exit 3).
func (m *Modifier) Apply(s *stack.Stack, layers []Layer) error {
	if m.Store.HasJournal(stack.ModifyJournal) {
		return exitcode.New(exitcode.RebaseInProgress, "a modify is already in progress; run `glab stax modify --continue` or `--abort`")
	}
	if s.Bottom() == nil {
		return exitcode.New(exitcode.Generic, "every layer is merged; nothing to modify")
	}
	if err := m.validate(s, layers); err != nil {
		return err
	}
	ranges, err := m.Ranges(s)
	if err != nil {
		return err
	}
	if err := m.CheckLinear(ranges); err != nil {
		return err
	}
	cur, err := m.Repo.CurrentBranch()
	if err != nil {
		return err
	}
	top, err := m.Repo.TopLevel()
	if err != nil {
		return err
	}
	st := &modifyState{
		StackKey: stackKey(s), OriginalBranch: cur, OriginalDir: top,
		Layers: layers, Ranges: ranges, NewTips: map[string]string{},
		Start: ranges[s.Bottom().Name].Base,
	}
	if err := m.Store.SaveJournal(stack.ModifyJournal, st); err != nil {
		return err
	}
	return m.at(top).build(s, st, 0)
}

// Continue resumes after the user resolved a cherry-pick conflict, in the
// worktree the modify started in, whichever worktree it is run from.
func (m *Modifier) Continue(f *stack.File) error {
	st, s, err := m.load(f)
	if err != nil {
		return err
	}
	w := m.at(st.OriginalDir)
	layer := st.Layers[st.Next].Name
	resumed := false
	if w.cherryPicking() {
		if files, _ := w.Repo.ConflictedFiles(); len(files) > 0 {
			w.reportConflict(layer, files)
			return exitcode.New(exitcode.RebaseConflict, "resolve the conflicts and `git add` the files first")
		}
		if err := w.continuePick(); err != nil {
			return w.conflict(st, err)
		}
		resumed = true
	}
	head, err := w.Repo.RevParse("HEAD")
	if err != nil {
		return err
	}
	if !resumed {
		// No cherry-pick was paused, so check what happened to the layer.
		switch {
		case st.PickStart == "" || head == st.PickStart:
			// Its cherry-picks never landed: git refused to start them, or they
			// were aborted with git. Build the layer again.
			w.Log("Rebuilding %s", layer)
			return w.build(s, st, st.Next)
		case !w.Repo.IsAncestor(st.PickStart, head):
			return exitcode.New(exitcode.Generic, "HEAD in %s moved away from the layer being rebuilt; check out %s there again, or run `glab stax modify --abort`", st.OriginalDir, short(st.PickStart))
		}
		// The picks were finished with git commands.
	}
	st.NewTips[layer] = head
	return w.build(s, st, st.Next+1)
}

// Abort cancels a paused modify. Branches were not moved yet, so only the
// cherry-pick and the checkout need undoing.
func (m *Modifier) Abort() error {
	var st modifyState
	ok, err := m.Store.LoadJournal(stack.ModifyJournal, &st)
	if err != nil {
		return err
	}
	if !ok {
		return exitcode.New(exitcode.Generic, "no modify in progress")
	}
	w := m.at(st.OriginalDir)
	if w.cherryPicking() {
		if _, err := w.Repo.Run("cherry-pick", "--abort"); err != nil {
			return err
		}
	}
	if st.OriginalBranch != "" {
		if err := w.Repo.Checkout(st.OriginalBranch); err != nil {
			return err
		}
	}
	m.Log("Modify aborted; nothing was changed")
	return m.Store.ClearJournal(stack.ModifyJournal)
}

// continuePick resumes the cherry-pick. A commit that became empty through
// the conflict resolution is skipped, as it no longer changes anything. Git
// runs in the C locale so that its "now empty" message can be recognized.
func (m *Modifier) continuePick() error {
	env := []string{"GIT_EDITOR=true", "LC_ALL=C"}
	_, err := m.Repo.RunEnv(env, "cherry-pick", "--continue")
	for err != nil && strings.Contains(err.Error(), "now empty") {
		_, err = m.Repo.RunEnv(env, "cherry-pick", "--skip")
	}
	return err
}

func (m *Modifier) load(f *stack.File) (*modifyState, *stack.Stack, error) {
	var st modifyState
	ok, err := m.Store.LoadJournal(stack.ModifyJournal, &st)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, exitcode.New(exitcode.Generic, "no modify in progress")
	}
	s := f.Find(st.StackKey)
	if s == nil {
		return nil, nil, exitcode.New(exitcode.NotInStack, "the stack being modified no longer exists; run `glab stax modify --abort`")
	}
	return &st, s, nil
}

func stackKey(s *stack.Stack) string { return s.Branches[0].Name }

func (m *Modifier) validate(s *stack.Stack, layers []Layer) error {
	if len(layers) == 0 {
		return exitcode.New(exitcode.InvalidArgs, "a stack needs at least one layer; use `glab stax unstack` to remove it")
	}
	names := map[string]bool{}
	used := map[string]bool{}
	for _, l := range layers {
		if names[l.Name] {
			return exitcode.New(exitcode.InvalidArgs, "%q appears twice", l.Name)
		}
		names[l.Name] = true
		if !m.Repo.ValidBranchName(l.Name) || l.Name == s.Trunk {
			return exitcode.New(exitcode.InvalidArgs, "%q is not a usable branch name", l.Name)
		}
		// A new name must be free: renaming onto a branch that still exists (even
		// a folded or dropped layer's) would fail halfway through, and an
		// inserted layer would take over that branch.
		if l.Name != l.Orig && m.Repo.BranchExists(l.Name) {
			return exitcode.New(exitcode.InvalidArgs, "branch %q already exists", l.Name)
		}
		for _, src := range l.Sources {
			b := s.Get(src)
			if b == nil || b.Merged || used[src] {
				return exitcode.New(exitcode.InvalidArgs, "invalid source layer %q", src)
			}
			used[src] = true
		}
	}
	return nil
}

// pickHead reports whether a cherry-pick stopped on a conflict.
func (m *Modifier) pickHead() bool {
	_, err := m.Repo.Run("rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	return err == nil
}

// sequencerActive reports whether a multi-commit cherry-pick is unfinished.
func (m *Modifier) sequencerActive() bool {
	dir, err := m.Repo.GitDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "sequencer", "todo"))
	return err == nil
}

func (m *Modifier) cherryPicking() bool { return m.pickHead() || m.sequencerActive() }

// build creates each layer from st.Layers[from:] on top of the one below,
// then moves the branches and updates s.
func (m *Modifier) build(s *stack.Stack, st *modifyState, from int) error {
	for i := from; i < len(st.Layers); i++ {
		l := st.Layers[i]
		start := st.Start
		if i > 0 {
			start = st.NewTips[st.Layers[i-1].Name]
		}
		// A layer that still starts with its own commits on an unchanged base
		// keeps them; only commits added on top are cherry-picked.
		sources := l.Sources
		if len(sources) > 0 && sources[0] == l.Orig && st.Ranges[l.Orig].Base == start {
			start, sources = st.Ranges[l.Orig].Tip, sources[1:]
		}
		if len(sources) == 0 {
			st.NewTips[l.Name] = start
			continue
		}
		if _, err := m.Repo.Run("checkout", "--quiet", "--detach", start); err != nil {
			return fmt.Errorf("building %s failed: %w; fix the problem, then run `glab stax modify --continue` (or `--abort`)", l.Name, err)
		}
		st.Next, st.PickStart = i, start
		if err := m.Store.SaveJournal(stack.ModifyJournal, st); err != nil {
			return err
		}
		// List each source's commits explicitly: several A..B ranges in one
		// cherry-pick would be combined as a single set and drop commits.
		var picks []string
		for _, src := range sources {
			r := st.Ranges[src]
			out, err := m.Repo.Run("rev-list", "--reverse", r.Base+".."+r.Tip)
			if err != nil {
				return err
			}
			if out != "" {
				picks = append(picks, strings.Split(out, "\n")...)
			}
		}
		if len(picks) > 0 {
			args := append([]string{"cherry-pick", "--allow-empty", "--keep-redundant-commits"}, picks...)
			if _, err := m.Repo.Run(args...); err != nil {
				return m.conflict(st, err)
			}
		}
		head, err := m.Repo.RevParse("HEAD")
		if err != nil {
			return err
		}
		st.NewTips[l.Name] = head
		m.Log("Rebuilt %s", l.Name)
	}
	return m.finish(s, st)
}

func (m *Modifier) conflict(st *modifyState, err error) error {
	layer := st.Layers[st.Next].Name
	if !m.pickHead() {
		// Not a conflict: git refused to pick a commit at all (e.g. untracked
		// files in the way). Undo the partial sequence so that --continue builds
		// the layer again; continuing git's sequence would skip that commit.
		if m.sequencerActive() {
			_, _ = m.Repo.Run("cherry-pick", "--abort")
		}
		return fmt.Errorf("building %s failed: %w; fix the problem, then run `glab stax modify --continue` (or `--abort`)", layer, err)
	}
	files, _ := m.Repo.ConflictedFiles()
	m.reportConflict(layer, files)
	return exitcode.New(exitcode.RebaseConflict, "conflict while rebuilding %s", layer)
}

func (m *Modifier) reportConflict(layer string, files []string) {
	m.Log("Conflict while rebuilding %s:", layer)
	for _, f := range files {
		m.Log("  %s", f)
	}
	m.Log("Resolve the conflicts, `git add` the files, then run `glab stax modify --continue`.")
	m.Log("Or cancel the whole modify with `glab stax modify --abort`.")
}

// finish moves branches to their new tips, applies renames, updates the
// stack metadata and restores a sensible checkout.
func (m *Modifier) finish(s *stack.Stack, st *modifyState) error {
	g := m.Repo
	if err := m.checkMovable(st); err != nil {
		return err
	}
	old := map[string]*stack.Branch{}
	for _, b := range s.Branches {
		old[b.Name] = b
	}
	var merged, active []*stack.Branch
	for _, b := range s.Branches {
		if b.Merged {
			merged = append(merged, b)
		}
	}
	parentTip := st.Start
	for _, l := range st.Layers {
		tip := st.NewTips[l.Name]
		if err := m.moveBranch(l, tip, st.OriginalDir); err != nil {
			return err
		}
		b := &stack.Branch{Name: l.Name, Base: parentTip}
		if ob := old[l.Orig]; ob != nil && l.Orig == l.Name {
			b.MR = ob.MR
		} else if ob != nil && ob.MR != nil {
			m.Log("Warning: !%d still has %s as its source branch; `glab stax submit` opens a new MR for %s. Close !%d on GitLab.", ob.MR.IID, l.Orig, l.Name, ob.MR.IID)
		}
		active = append(active, b)
		parentTip = tip
	}
	kept := map[string]bool{}
	for _, l := range st.Layers {
		kept[l.Orig] = true
	}
	for _, b := range s.Active() {
		if !kept[b.Name] && b.MR != nil {
			m.Log("Note: %s left the stack; its branch and !%d are unchanged", b.Name, b.MR.IID)
		}
	}
	s.Branches = append(merged, active...)

	// Check out the original branch, following renames, else the nearest
	// survivor. The branches have moved already, so failing here is only a
	// warning.
	dest := st.OriginalBranch
	for _, l := range st.Layers {
		if l.Orig == st.OriginalBranch {
			dest = l.Name
		}
	}
	if s.Get(dest) == nil && dest != s.Trunk {
		dest = s.Top().Name
	}
	owner, err := g.WorktreeFor(dest)
	if err == nil && owner != "" && !git.SamePath(owner, st.OriginalDir) {
		dest = s.Trunk
	}
	if err == nil {
		err = g.At(st.OriginalDir).Checkout(dest)
	}
	if err != nil {
		m.Log("Warning: could not check out %s in %s: %v", dest, st.OriginalDir, err)
	}
	m.Log("Stack is now: %s", strings.Join(layerNames(st.Layers), " → "))
	m.Log("Run `glab stax submit` to update the merge requests.")
	return m.Store.ClearJournal(stack.ModifyJournal)
}

// checkMovable fails before any branch moves if a layer branch checked out in
// another worktree has to move while that worktree has uncommitted changes:
// it may have changed since the modify started or paused.
func (m *Modifier) checkMovable(st *modifyState) error {
	for _, l := range st.Layers {
		owner, err := m.Repo.WorktreeFor(l.Name)
		if err != nil {
			return err
		}
		if owner == "" || git.SamePath(owner, st.OriginalDir) {
			continue
		}
		wt := m.Repo.At(owner)
		if head, _ := wt.RevParse("HEAD"); head == st.NewTips[l.Name] {
			continue
		}
		if clean, err := wt.IsClean(); err != nil || !clean {
			return exitcode.New(exitcode.Generic, "%s is checked out in %s, which has uncommitted changes; commit or stash them, then run `glab stax modify --continue`", l.Name, owner)
		}
	}
	return nil
}

// moveBranch points l's branch at tip, creating or renaming it as needed. It
// can be repeated after a failure: a rename that already happened is skipped.
func (m *Modifier) moveBranch(l Layer, tip, here string) error {
	g := m.Repo
	if l.Orig != "" && l.Orig != l.Name && g.BranchExists(l.Orig) {
		owner, err := g.WorktreeFor(l.Orig)
		if err != nil {
			return err
		}
		if owner != "" && !git.SamePath(owner, here) {
			// Renaming a branch checked out elsewhere would surprise that worktree.
			m.Log("Warning: %s is checked out in %s; created %s and kept %s", l.Orig, owner, l.Name, l.Orig)
			if g.BranchExists(l.Name) {
				return g.SetBranchSHA(l.Name, tip)
			}
			return g.CreateBranch(l.Name, tip)
		}
		if _, err := g.Run("branch", "-m", l.Orig, l.Name); err != nil {
			return err
		}
	}
	if !g.BranchExists(l.Name) {
		return g.CreateBranch(l.Name, tip)
	}
	owner, err := g.WorktreeFor(l.Name)
	if err != nil {
		return err
	}
	if owner != "" {
		if cur, _ := g.At(owner).RevParse("HEAD"); cur == tip {
			return nil
		}
		// --keep refuses rather than discard uncommitted changes.
		_, err = g.At(owner).Run("reset", "--keep", "--quiet", tip)
		return err
	}
	return g.SetBranchSHA(l.Name, tip)
}

func layerNames(ls []Layer) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}
