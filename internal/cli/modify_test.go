package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// modify runs `modify` with scripted editor keys.
func (h *harness) modify(keys ...string) result {
	h.t.Helper()
	h.keys = keys
	return h.runIn(h.repo.Dir, "", true, "modify")
}

func (h *harness) files(ref string) string {
	return strings.ReplaceAll(h.repo.Git("ls-tree", "--name-only", ref), "\n", " ")
}

// The editor lists layers top first and starts on c: j moves down to b.

func TestModifyDrop(t *testing.T) {
	h := threeLayers(t)
	oldB := h.sha("b")
	if r := h.modify("j", "x", "ctrl+s"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,c" {
		t.Fatalf("stack = %v", got)
	}
	h.assertStacked("a", "c", "1")
	if f := h.files("c"); strings.Contains(f, "b.txt") || !strings.Contains(f, "c.txt") {
		t.Fatalf("c files = %s", f)
	}
	if h.sha("b") != oldB {
		t.Fatal("dropped branch was changed")
	}
	if h.current() != "c" {
		t.Fatalf("on %s", h.current())
	}
}

func TestModifyFoldDownAndUp(t *testing.T) {
	h := threeLayers(t)
	oldA := h.sha("a")
	if r := h.modify("j", "d", "ctrl+s"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,c" {
		t.Fatalf("stack = %v", got)
	}
	h.assertStacked("main", "a", "3")
	h.assertStacked("a", "c", "1")
	if h.repo.Git("rev-parse", "a~2") != oldA {
		t.Fatal("fold down rewrote a's own commit")
	}

	h2 := threeLayers(t)
	if r := h2.modify("j", "u", "ctrl+s"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h2.file().Stacks[0]); strings.Join(got, ",") != "a,c" {
		t.Fatalf("stack = %v", got)
	}
	h2.assertStacked("a", "c", "3")
}

func TestModifyReorderAndInsert(t *testing.T) {
	h := threeLayers(t)
	// Move c below b, then insert "mid" above a.
	if r := h.modify("J", "j", "I", "m", "i", "d", "enter", "ctrl+s"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,mid,c,b" {
		t.Fatalf("stack = %v", got)
	}
	if h.sha("mid") != h.sha("a") {
		t.Fatal("inserted layer should start empty on a")
	}
	h.assertStacked("mid", "c", "1")
	h.assertStacked("c", "b", "2")
	s := h.file().Stacks[0]
	if s.Get("b").Base != h.sha("c") {
		t.Fatal("bases not updated")
	}
}

func TestModifyRenameKeepsCheckout(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "b")
	if err := mutateFile(h, func(f *stack.File) { f.Stacks[0].Get("b").MR = &stack.MR{IID: 7} }); err != nil {
		t.Fatal(err)
	}
	// Cursor starts on the top (c); j moves to b.
	r := h.modify("j", "r", "e", "e", "enter", "ctrl+s")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if h.current() != "bee" || h.repo.Git("branch", "--list", "b") != "" {
		t.Fatalf("on %s; branches: %s", h.current(), h.repo.Git("branch"))
	}
	if s := h.file().Stacks[0]; s.Get("bee") == nil || s.Get("bee").MR != nil {
		t.Fatalf("renamed layer = %+v", s.Get("bee"))
	}
	if !strings.Contains(r.err, "Close !7") {
		t.Fatalf("no warning about the old MR:\n%s", r.err)
	}
}

func TestModifyUndoAndCancel(t *testing.T) {
	h := threeLayers(t)
	before := h.sha("c")
	r := h.modify("x", "z", "ctrl+s", "q")
	if r.e != nil || !strings.Contains(r.err, "No changes made") || h.sha("c") != before {
		t.Fatalf("%v\n%s", r.e, r.err)
	}
	if got := exitcode.Of(h.run("modify").e); got != exitcode.InvalidArgs {
		t.Fatalf("non-interactive: exit %d", got)
	}
	writeFile(t, h.repo.Dir+"/c.txt", "dirty\n")
	if r := h.modify("x", "ctrl+s"); r.e == nil {
		t.Fatal("dirty worktree accepted")
	}
}

// swapConflict makes moving b below a conflict: b edits a.txt, which only a adds.
func swapConflict(t *testing.T) *harness {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "b")
	h.repo.Commit("a.txt", "from b\n", "b edits a.txt")
	h.repo.Git("checkout", "--quiet", "c")
	h.must("rebase", "--no-trunk")
	return h
}

func TestModifyConflictContinue(t *testing.T) {
	h := swapConflict(t)
	r := h.modify("j", "J", "ctrl+s") // b moves below a
	if exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	if got := exitcode.Of(h.run("add", "x").e); got != exitcode.RebaseInProgress {
		t.Fatalf("add while paused: exit %d", got)
	}
	writeFile(t, h.repo.Dir+"/a.txt", "resolved\n")
	h.repo.Git("add", "a.txt")
	r = h.run("modify", "--continue")
	for i := 0; exitcode.Of(r.e) == exitcode.RebaseConflict; i++ { // a's own commit conflicts too
		if i == 5 {
			t.Fatalf("still conflicting after %d continues:\n%s", i, r.err)
		}
		writeFile(t, h.repo.Dir+"/a.txt", fmt.Sprintf("resolved again %d\n", i))
		h.repo.Git("add", "a.txt")
		r = h.run("modify", "--continue")
	}
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("stack = %v", got)
	}
	h.assertStacked("b", "a", "1")
	h.assertStacked("a", "c", "1")
}

func TestModifyConflictAbort(t *testing.T) {
	h := swapConflict(t)
	before := map[string]string{"a": h.sha("a"), "b": h.sha("b"), "c": h.sha("c")}
	if r := h.modify("j", "J", "ctrl+s"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d", exitcode.Of(r.e))
	}
	h.must("modify", "--abort")
	for b, sha := range before {
		if h.sha(b) != sha {
			t.Errorf("%s moved", b)
		}
	}
	if h.current() != "c" || h.repo.Git("status", "--porcelain") != "" {
		t.Fatal("checkout not restored")
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("stack = %v", got)
	}
}

func TestModifyDroppingCurrentChecksOutTop(t *testing.T) {
	h := threeLayers(t)
	if r := h.modify("x", "ctrl+s"); r.e != nil { // drop c, the current branch
		t.Fatal(r.e, r.err)
	}
	if h.current() != "b" {
		t.Fatalf("on %s", h.current())
	}
}

// After `git cherry-pick --abort` by hand, --continue builds the layer again
// instead of dropping its commits.
func TestModifyContinueAfterManualAbort(t *testing.T) {
	h := swapConflict(t)
	oldB := h.sha("b")
	if r := h.modify("j", "J", "ctrl+s"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	h.repo.Git("cherry-pick", "--abort")
	if r := h.run("modify", "--continue"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("--continue after a manual abort: exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	if h.sha("b") != oldB {
		t.Fatal("b moved while the modify is paused")
	}
	h.must("modify", "--abort")
	if h.sha("b") != oldB || h.count("main", "b") != "4" {
		t.Fatal("b lost commits")
	}
}

// --continue from another worktree finishes the modify where it started.
func TestModifyContinueFromOtherWorktree(t *testing.T) {
	h := swapConflict(t)
	wt := filepath.Join(t.TempDir(), "wt")
	h.repo.Git("worktree", "add", "--quiet", "--detach", wt, "main")
	if r := h.modify("j", "J", "ctrl+s"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	r := h.runIn(wt, "", false, "modify", "--continue")
	for i := 0; exitcode.Of(r.e) == exitcode.RebaseConflict; i++ {
		if i == 5 {
			t.Fatalf("still conflicting:\n%s", r.err)
		}
		writeFile(t, h.repo.Dir+"/a.txt", fmt.Sprintf("resolved %d\n", i))
		h.repo.Git("add", "a.txt")
		r = h.runIn(wt, "", false, "modify", "--continue")
	}
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "b,a,c" {
		t.Fatalf("stack = %v", got)
	}
	h.assertStacked("b", "a", "1")
	h.assertStacked("a", "c", "1")
	if h.repo.Git("-C", wt, "rev-parse", "HEAD") != h.sha("main") {
		t.Fatal("the other worktree was used to build layers")
	}
}

// The remaining layers were merged on GitLab before modify ran.
func TestModifyWhenEverythingMergedRemotely(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")
	h.must("submit", "--auto")
	h.gl.MRs[1].State = "merged"
	r := h.modify("I", "n", "ctrl+s")
	if r.e == nil || !strings.Contains(r.e.Error(), "every layer is merged") {
		t.Fatalf("err = %v", r.e)
	}
}

// A bad name is refused in the editor, so the other staged changes survive.
func TestModifyRefusesExistingBranchNameInEditor(t *testing.T) {
	h := threeLayers(t)
	// Fold c into b, then try to rename a to "c" (c's branch still exists).
	r := h.modify("d", "j", "r", "backspace", "c", "enter", "ctrl+s")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,b" {
		t.Fatalf("stack = %v", got)
	}
	h.assertStacked("a", "b", "3")
}

// A layer restacked by hand onto newer parent commits keeps only its own
// commit when rebuilt.
func TestModifyAfterManualRestack(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "b")
	h.repo.Commit("b3.txt", "b\n", "b3")
	h.repo.Git("checkout", "--quiet", "c")
	h.repo.Git("rebase", "--quiet", "b")
	if r := h.modify("r", "2", "enter", "ctrl+s"); r.e != nil { // rename c to c2
		t.Fatal(r.e, r.err)
	}
	h.assertStacked("b", "c2", "1")
}
