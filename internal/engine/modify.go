package engine

import (
	"fmt"
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
	OriginalDir    string                 `json:"originalDir"`
	Layers         []Layer                `json:"layers"` // bottom first
	Ranges         map[string]commitRange `json:"ranges"`
	Start          string                 `json:"start"`   // where the bottom layer starts
	NewTips        map[string]string      `json:"newTips"` // by layer name, as layers are built
	Next           int                    `json:"next"`    // layer being built
}

// Modifier applies a restructured layer list to a stack.
type Modifier struct {
	Repo  *git.Repo
	Store stack.Store
	Log   func(format string, args ...any)
}

// Ranges returns each active layer's own commits, keyed by branch.
func (m *Modifier) Ranges(s *stack.Stack) (map[string]commitRange, error) {
	out := map[string]commitRange{}
	for _, b := range s.Active() {
		tip, err := m.Repo.RevParse("refs/heads/" + b.Name)
		if err != nil {
			return nil, err
		}
		base := b.Base
		if base == "" || !m.Repo.IsAncestor(base, tip) {
			if base, err = m.Repo.MergeBase(s.Parent(b.Name), tip); err != nil {
				return nil, err
			}
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
	return m.build(s, st, 0)
}

// Continue resumes after the user resolved a cherry-pick conflict.
func (m *Modifier) Continue(f *stack.File) error {
	st, s, err := m.load(f)
	if err != nil {
		return err
	}
	if m.cherryPicking() {
		if files, _ := m.Repo.ConflictedFiles(); len(files) > 0 {
			m.reportConflict(st.Layers[st.Next].Name, files)
			return exitcode.New(exitcode.RebaseConflict, "resolve the conflicts and `git add` the files first")
		}
		if err := m.continuePick(); err != nil {
			return m.conflict(st, err)
		}
	}
	head, err := m.Repo.RevParse("HEAD")
	if err != nil {
		return err
	}
	st.NewTips[st.Layers[st.Next].Name] = head
	return m.build(s, st, st.Next+1)
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
	if m.cherryPicking() {
		if _, err := m.Repo.Run("cherry-pick", "--abort"); err != nil {
			return err
		}
	}
	if st.OriginalBranch != "" {
		if err := m.Repo.At(st.OriginalDir).Checkout(st.OriginalBranch); err != nil {
			return err
		}
	}
	m.Log("Modify aborted; nothing was changed")
	return m.Store.ClearJournal(stack.ModifyJournal)
}

// continuePick resumes the cherry-pick. A commit that became empty through
// the conflict resolution is skipped, as it no longer changes anything.
func (m *Modifier) continuePick() error {
	_, err := m.Repo.RunEnv([]string{"GIT_EDITOR=true"}, "cherry-pick", "--continue")
	for err != nil && strings.Contains(err.Error(), "now empty") {
		_, err = m.Repo.RunEnv([]string{"GIT_EDITOR=true"}, "cherry-pick", "--skip")
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
		if l.Name != l.Orig && m.Repo.BranchExists(l.Name) && s.Get(l.Name) == nil {
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

func (m *Modifier) cherryPicking() bool {
	dir, err := m.Repo.GitDir()
	if err != nil {
		return false
	}
	_, err = m.Repo.Run("rev-parse", "--verify", "--quiet", "CHERRY_PICK_HEAD")
	if err == nil {
		return true
	}
	return fileExists(dir + "/sequencer/todo")
}

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
			return err
		}
		st.Next = i
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
	if !m.cherryPicking() {
		return fmt.Errorf("building %s failed: %w", st.Layers[st.Next].Name, err)
	}
	files, _ := m.Repo.ConflictedFiles()
	m.reportConflict(st.Layers[st.Next].Name, files)
	return exitcode.New(exitcode.RebaseConflict, "conflict while rebuilding %s", st.Layers[st.Next].Name)
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

	// Check out the original branch, following renames, else the nearest survivor.
	dest := st.OriginalBranch
	for _, l := range st.Layers {
		if l.Orig == st.OriginalBranch {
			dest = l.Name
		}
	}
	if s.Get(dest) == nil && dest != s.Trunk {
		dest = s.Top().Name
	}
	wt := g.At(st.OriginalDir)
	if owner, _ := g.WorktreeFor(dest); owner == "" || owner == st.OriginalDir {
		if err := wt.Checkout(dest); err != nil {
			return err
		}
	} else if err := wt.Checkout(s.Trunk); err != nil {
		return err
	}
	m.Log("Stack is now: %s", strings.Join(layerNames(st.Layers), " → "))
	m.Log("Run `glab stax submit` to update the merge requests.")
	return m.Store.ClearJournal(stack.ModifyJournal)
}

// moveBranch points l's branch at tip, creating or renaming it as needed.
func (m *Modifier) moveBranch(l Layer, tip, here string) error {
	g := m.Repo
	if l.Orig != "" && l.Orig != l.Name {
		owner, err := g.WorktreeFor(l.Orig)
		if err != nil {
			return err
		}
		if owner != "" && !samePath(owner, here) {
			// Renaming a branch checked out elsewhere would surprise that worktree.
			m.Log("Warning: %s is checked out in %s; created %s and kept %s", l.Orig, owner, l.Name, l.Orig)
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
		_, err = g.At(owner).Run("reset", "--hard", "--quiet", tip)
		return err
	}
	return g.SetBranchSHA(l.Name, tip)
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

func layerNames(ls []Layer) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Name
	}
	return out
}
