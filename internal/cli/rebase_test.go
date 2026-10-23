package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func (h *harness) sha(ref string) string { return h.repo.Git("rev-parse", ref) }

func (h *harness) count(base, head string) string {
	return h.repo.Git("rev-list", "--count", base+".."+head)
}

func (h *harness) assertStacked(lower, upper, ownCommits string) {
	h.t.Helper()
	if h.repo.Git("merge-base", lower, upper) != h.sha(lower) {
		h.t.Fatalf("%s is not on top of %s", upper, lower)
	}
	if got := h.count(lower, upper); got != ownCommits {
		h.t.Fatalf("%s has %s commits over %s, want %s", upper, got, lower, ownCommits)
	}
}

func TestRebaseOntoMovedTrunk(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "b")
	upstream := h.repo.PushFromElsewhere("main", "up.txt", "up\n", "upstream change")

	h.must("rebase")

	if h.sha("main") != upstream {
		t.Fatal("local main was not fast-forwarded")
	}
	h.assertStacked("main", "a", "1")
	h.assertStacked("a", "b", "2")
	h.assertStacked("b", "c", "1")
	if h.current() != "b" {
		t.Fatalf("checkout not restored: on %s", h.current())
	}
	s := h.file().Stacks[0]
	if s.Branches[0].Base != upstream || s.Branches[2].Base != h.sha("b") {
		t.Fatalf("bases not updated: %+v", s.Branches)
	}
}

func TestRebaseAfterAmendingLowerLayer(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended")

	h.must("rebase", "--no-trunk")

	h.assertStacked("a", "b", "2")
	h.assertStacked("b", "c", "1")
	if strings.Contains(h.repo.Git("log", "--format=%s", "main..c"), "a1\n") {
		t.Fatal("old a1 commit is still in c")
	}
}

func conflictSetup(t *testing.T) *harness {
	h := threeLayers(t)
	// b edits a.txt; then a is amended to edit the same line differently.
	h.repo.Git("checkout", "--quiet", "b")
	h.repo.Commit("a.txt", "from b\n", "b edits a.txt")
	h.repo.Git("checkout", "--quiet", "a")
	writeFile(t, h.repo.Dir+"/a.txt", "from a\n")
	h.repo.Git("commit", "--quiet", "-am", "a edits a.txt")
	return h
}

func TestRebaseConflictContinue(t *testing.T) {
	h := conflictSetup(t)
	r := h.run("rebase", "--no-trunk")
	if exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d, want conflict: %v", exitcode.Of(r.e), r.e)
	}
	if !strings.Contains(r.err, "a.txt (lines 1)") {
		t.Fatalf("conflict report missing file/line:\n%s", r.err)
	}
	if got := exitcode.Of(h.run("add", "x").e); got != exitcode.RebaseInProgress {
		t.Fatalf("add during paused rebase: exit %d", got)
	}
	if got := exitcode.Of(h.run("rebase").e); got != exitcode.RebaseInProgress {
		t.Fatalf("second rebase: exit %d", got)
	}
	if got := exitcode.Of(h.run("rebase", "--continue").e); got != exitcode.RebaseConflict {
		t.Fatalf("continue before resolving: exit %d", got)
	}

	writeFile(t, h.repo.Dir+"/a.txt", "resolved\n")
	h.repo.Git("add", "a.txt")
	h.must("rebase", "--continue")

	h.assertStacked("a", "b", "3")
	h.assertStacked("b", "c", "1")
	if h.current() != "a" {
		t.Fatalf("checkout not restored: on %s", h.current())
	}
	if (stack.Store{Dir: h.repo.Dir + "/.git"}).HasJournal(stack.RebaseJournal) {
		t.Fatal("journal not cleared")
	}
}

func TestRebaseAbortRestoresEverything(t *testing.T) {
	h := conflictSetup(t)
	before := map[string]string{"a": h.sha("a"), "b": h.sha("b"), "c": h.sha("c")}
	if got := exitcode.Of(h.run("rebase", "--no-trunk").e); got != exitcode.RebaseConflict {
		t.Fatalf("exit %d", got)
	}
	h.must("rebase", "--abort")
	for b, sha := range before {
		if h.sha(b) != sha {
			t.Errorf("%s not restored", b)
		}
	}
	if h.current() != "a" || h.repo.Git("status", "--porcelain") != "" {
		t.Fatalf("on %s, status %q", h.current(), h.repo.Git("status", "--porcelain"))
	}
	if got := exitcode.Of(h.run("rebase", "--abort").e); got != exitcode.Generic {
		t.Fatalf("abort with nothing paused: exit %d", got)
	}
}

func TestRebaseSkipsSquashMergedLayer(t *testing.T) {
	h := threeLayers(t)
	// a's change lands on the remote trunk as a squash commit.
	h.repo.PushFromElsewhere("main", "a.txt", "a\n", "Squashed a (!1)")
	if err := mutateFile(h, func(f *stack.File) { f.Stacks[0].Branches[0].Merged = true }); err != nil {
		t.Fatal(err)
	}

	h.must("rebase")

	h.assertStacked("main", "b", "2")
	h.assertStacked("b", "c", "1")
}

func TestRebaseRanges(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended")
	h.repo.Git("checkout", "--quiet", "b")
	oldC := h.sha("c")

	h.must("rebase", "--no-trunk", "--downstack")
	h.assertStacked("a", "b", "2")
	if h.sha("c") != oldC {
		t.Fatal("--downstack moved c")
	}

	h.must("rebase", "--no-trunk", "--upstack")
	h.assertStacked("b", "c", "1")

	if got := exitcode.Of(h.run("rebase", "--upstack", "--downstack").e); got != exitcode.InvalidArgs {
		t.Fatalf("--upstack --downstack: exit %d", got)
	}
}

func TestRebaseInOtherWorktree(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended")
	wt := filepath.Join(t.TempDir(), "wt-c")
	h.repo.Git("worktree", "add", "--quiet", wt, "c")

	writeFile(t, wt+"/c.txt", "dirty\n")
	if r := h.run("rebase", "--no-trunk"); r.e == nil || !strings.Contains(r.e.Error(), "uncommitted") {
		t.Fatalf("dirty worktree not rejected: %v", r.e)
	}
	h.repo.Git("-C", wt, "checkout", "--quiet", "c.txt")

	h.must("rebase", "--no-trunk")
	h.assertStacked("b", "c", "1")
	if head := h.repo.Git("-C", wt, "rev-parse", "HEAD"); head != h.sha("c") {
		t.Fatal("worktree HEAD does not match rebased c")
	}
	if st := h.repo.Git("-C", wt, "status", "--porcelain"); st != "" {
		t.Fatalf("worktree left dirty: %q", st)
	}
}

func TestRebasePreserveDates(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended", "--date=2001-01-01T00:00:00Z")
	h.must("rebase", "--no-trunk", "--preserve-dates")
	dates := h.repo.Git("log", "-1", "--format=%at %ct", "b")
	parts := strings.Fields(dates)
	if parts[0] != parts[1] {
		t.Fatalf("author/committer dates differ: %s", dates)
	}
}

// a was squash-merged on GitLab (not yet known locally) and b conflicts with
// the trunk: after resolving, b's base is the trunk and a stays merged.
func TestRebaseContinueKeepsMergedFlags(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	h.repo.Git("checkout", "--quiet", "b")
	h.repo.Commit("a.txt", "from b\n", "b edits a.txt")
	h.must("push")
	h.repo.PushFromElsewhere("main", "a.txt", "a\n", "a1 (squashed !1)")
	h.repo.PushFromElsewhere("main", "a.txt", "upstream\n", "upstream edits a.txt")
	h.gl.MRs[1].State = "merged"

	if r := h.run("rebase"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	writeFile(t, h.repo.Dir+"/a.txt", "resolved\n")
	h.repo.Git("add", "a.txt")
	h.must("rebase", "--continue")
	s := h.file().Stacks[0]
	if !s.Branches[0].Merged || s.Branches[1].Base != h.sha("origin/main") {
		t.Fatalf("a merged = %v, b base = %s, want %s", s.Branches[0].Merged, s.Branches[1].Base, h.sha("origin/main"))
	}
}

// After `git rebase --abort` by hand, --continue rebases the layer again
// instead of reporting it done.
func TestRebaseContinueAfterManualAbort(t *testing.T) {
	h := conflictSetup(t)
	if r := h.run("rebase", "--no-trunk"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	h.repo.Git("rebase", "--abort")
	if r := h.run("rebase", "--continue"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("--continue after a manual abort: exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	writeFile(t, h.repo.Dir+"/a.txt", "resolved\n")
	h.repo.Git("add", "a.txt")
	h.must("rebase", "--continue")
	h.assertStacked("a", "b", "3")
	h.assertStacked("b", "c", "1")
}

// A paused rebase is aborted while another worktree has uncommitted work.
func TestRebaseAbortKeepsOtherWorktreeChanges(t *testing.T) {
	h := threeLayers(t)
	h.repo.Git("checkout", "--quiet", "c")
	h.repo.Commit("a.txt", "from c\n", "c edits a.txt")
	h.repo.Git("checkout", "--quiet", "a")
	writeFile(t, h.repo.Dir+"/a.txt", "from a\n")
	h.repo.Git("commit", "--quiet", "-am", "a edits a.txt")
	wt := filepath.Join(t.TempDir(), "wt-b")
	h.repo.Git("worktree", "add", "--quiet", wt, "b")
	oldB := h.sha("b")

	if r := h.run("rebase", "--no-trunk"); exitcode.Of(r.e) != exitcode.RebaseConflict {
		t.Fatalf("exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	writeFile(t, wt+"/b.txt", "work in progress\n")
	h.must("rebase", "--abort")
	if got := h.repo.Git("-C", wt, "status", "--porcelain"); !strings.Contains(got, "b.txt") {
		t.Fatalf("uncommitted change in %s was discarded (status %q)", wt, got)
	}
	if h.sha("b") != oldB {
		t.Fatal("b not restored")
	}
}

// --no-trunk leaves the bottom unmerged layer alone, also above a merged one.
func TestRebaseNoTrunkKeepsBottomAboveMergedLayer(t *testing.T) {
	h := threeLayers(t)
	if err := mutateFile(h, func(f *stack.File) { f.Stacks[0].Branches[0].Merged = true }); err != nil {
		t.Fatal(err)
	}
	oldB := h.sha("b")
	h.must("rebase", "--no-trunk")
	if h.sha("b") != oldB {
		t.Fatal("--no-trunk moved the bottom unmerged layer")
	}
	h.assertStacked("b", "c", "1")
}

// An MR recorded for a layer that turns out to belong to another branch (an
// IID from another project) does not make the layer merged.
func TestRebaseIgnoresMRsOfOtherBranches(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	h.gl.MRs[2].SourceBranch, h.gl.MRs[2].State = "someone-else", "merged"
	h.must("rebase", "--no-trunk")
	if h.file().Stacks[0].Get("b").Merged {
		t.Fatal("b was marked merged by another branch's MR")
	}
}

// A pushed commit dropped on purpose stays dropped.
func TestRebaseKeepsDroppedPushedCommit(t *testing.T) {
	h := threeLayers(t)
	h.must("push")
	h.repo.Git("checkout", "--quiet", "b")
	h.repo.Git("reset", "--quiet", "--hard", "HEAD~1") // drop b2
	h.must("rebase")
	if strings.Contains(h.repo.Git("log", "--format=%s", "main..c"), "b2") {
		t.Fatal("the dropped commit came back")
	}
	h.assertStacked("a", "b", "1")
}
