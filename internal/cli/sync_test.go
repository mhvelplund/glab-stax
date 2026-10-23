package cli

import (
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// submitted returns three layers with MRs !1 (a), !2 (b), !3 (c), c checked out.
func submitted(t *testing.T) *harness {
	h := threeLayers(t)
	h.must("submit", "--auto")
	return h
}

func TestSyncRebasesOntoMovedTrunk(t *testing.T) {
	h := submitted(t)
	h.repo.PushFromElsewhere("main", "up.txt", "up\n", "upstream")

	r := h.must("sync")
	h.assertStacked("main", "a", "1")
	h.assertStacked("b", "c", "1")
	if !strings.HasPrefix(h.remoteSHA("c"), h.sha("c")) {
		t.Fatal("rebased c not pushed")
	}
	if !strings.Contains(r.err, "Stack synced") || !strings.Contains(r.err, "!2 b: draft") {
		t.Fatalf("unexpected report:\n%s", r.err)
	}
}

func TestSyncWithoutTrunkChangeDoesNotRebase(t *testing.T) {
	h := submitted(t)
	before := h.sha("c")
	h.must("sync")
	if h.sha("c") != before {
		t.Fatal("sync rebased although nothing moved")
	}
}

func TestSyncAfterSquashMergeAndPrune(t *testing.T) {
	h := submitted(t)
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.PushFromElsewhere("main", "a.txt", "a\n", "a1 (squashed !1)")
	h.gl.MRs[1].State = "merged"

	r := h.must("sync", "--prune")

	s := h.file().Stacks[0]
	if !s.Branches[0].Merged {
		t.Fatal("a not marked merged")
	}
	h.assertStacked("main", "b", "2")
	if h.gl.MRs[2].TargetBranch != "main" {
		t.Fatalf("!2 targets %s", h.gl.MRs[2].TargetBranch)
	}
	if h.repo.Git("branch", "--list", "a") != "" {
		t.Fatal("a not pruned")
	}
	if h.current() != "b" {
		t.Fatalf("on %s after pruning the current branch", h.current())
	}
	if !strings.Contains(r.err, "!1 a: merged") {
		t.Fatalf("report:\n%s", r.err)
	}
	// The merged MR stays listed in the open MRs' stack block.
	info, _ := glab.ParseBlock(h.gl.MRs[3].Description)
	if len(info.MRs) != 3 {
		t.Fatalf("block = %+v", info)
	}
	// A later rebase still works with a's branch gone.
	h.repo.PushFromElsewhere("main", "up.txt", "up\n", "more upstream")
	h.must("sync")
	h.assertStacked("main", "b", "2")
}

func TestSyncConflictRestoresEverything(t *testing.T) {
	h := submitted(t)
	h.repo.PushFromElsewhere("main", "a.txt", "conflicting\n", "upstream edits a.txt")
	before := map[string]string{"a": h.sha("a"), "b": h.sha("b"), "c": h.sha("c")}

	r := h.run("sync")
	if exitcode.Of(r.e) != exitcode.RebaseConflict || !strings.Contains(r.e.Error(), "glab stax rebase") {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	for b, sha := range before {
		if h.sha(b) != sha {
			t.Errorf("%s not restored", b)
		}
	}
	if (stack.Store{Dir: h.repo.Dir + "/.git"}).HasJournal(stack.RebaseJournal) {
		t.Fatal("journal left behind")
	}
	if h.current() != "c" || h.repo.Git("status", "--porcelain") != "" {
		t.Fatal("worktree not restored")
	}
}

// teammateAddsLayer pushes d on top of c from another clone and records it in the MR blocks.
func teammateAddsLayer(h *harness) {
	other := h.repo.Clone()
	other.Git("checkout", "--quiet", "-b", "d", "origin/c")
	other.Commit("d.txt", "d\n", "d1")
	other.Git("push", "--quiet", "origin", "d")
	h.gl.Add(glab.MR{IID: 4, Title: "d1", SourceBranch: "d", TargetBranch: "c"})
	info := glab.StackInfo{Trunk: "main", MRs: []glab.StackItem{{IID: 1, Branch: "a"}, {IID: 2, Branch: "b"}, {IID: 3, Branch: "c"}, {IID: 4, Branch: "d"}}}
	for _, mr := range h.gl.MRs {
		mr.Description = glab.SetBlock(mr.Description, glab.RenderBlock(info, mr.IID))
	}
}

func TestSyncPullsLayersAddedOnGitLab(t *testing.T) {
	h := submitted(t)
	teammateAddsLayer(h)

	h.must("sync")
	s := h.file().Stacks[0]
	if got := names(s); len(got) != 4 || got[3] != "d" || s.Branches[3].MR.IID != 4 {
		t.Fatalf("stack = %v", got)
	}
	h.must("top")
	if h.current() != "d" {
		t.Fatal("d not checked out by top")
	}
}

func TestSyncDiverged(t *testing.T) {
	h := submitted(t)
	// GitLab says the stack is a, b, x; locally it is a, b, c.
	h.gl.Add(glab.MR{IID: 9, Title: "x", SourceBranch: "x", TargetBranch: "b"})
	info := glab.StackInfo{Trunk: "main", MRs: []glab.StackItem{{IID: 1, Branch: "a"}, {IID: 2, Branch: "b"}, {IID: 9, Branch: "x"}}}
	for _, iid := range []int{1, 2, 9} {
		h.gl.MRs[iid].Description = glab.RenderBlock(info, iid)
	}
	other := h.repo.Clone()
	other.Git("checkout", "--quiet", "-b", "x", "origin/b")
	other.Commit("x.txt", "x\n", "x1")
	other.Git("push", "--quiet", "origin", "x")

	if got := exitcode.Of(h.run("sync").e); got != exitcode.Generic {
		t.Fatalf("non-interactive divergence: exit %d", got)
	}

	// Keep local: x's MR leaves the stack block, local MRs get the local stack.
	r := h.runIn(h.repo.Dir, "2\n", true, "sync")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if _, ok := glab.ParseBlock(h.gl.MRs[9].Description); ok {
		t.Fatal("x's MR still has a stack block")
	}
	got, _ := glab.ParseBlock(h.gl.MRs[1].Description)
	if len(got.MRs) != 3 || got.MRs[2].Branch != "c" {
		t.Fatalf("local stack not published: %+v", got)
	}
}

func TestSyncDivergedAdoptRemote(t *testing.T) {
	h := submitted(t)
	h.gl.Add(glab.MR{IID: 9, Title: "x", SourceBranch: "x", TargetBranch: "b"})
	info := glab.StackInfo{Trunk: "main", MRs: []glab.StackItem{{IID: 1, Branch: "a"}, {IID: 2, Branch: "b"}, {IID: 9, Branch: "x"}}}
	for _, iid := range []int{1, 2, 9} {
		h.gl.MRs[iid].Description = glab.RenderBlock(info, iid)
	}
	other := h.repo.Clone()
	other.Git("checkout", "--quiet", "-b", "x", "origin/b")
	other.Commit("x.txt", "x\n", "x1")
	other.Git("push", "--quiet", "origin", "x")
	h.repo.Git("checkout", "--quiet", "b")

	r := h.runIn(h.repo.Dir, "1\n", true, "sync")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); len(got) != 3 || got[2] != "x" {
		t.Fatalf("stack = %v", got)
	}
}

func TestSyncSingleMRSkipsBlocks(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")
	h.must("push")
	h.gl.Add(glab.MR{IID: 1, Title: "a1", SourceBranch: "a", TargetBranch: "main"})
	r := h.must("sync")
	if !strings.Contains(r.err, "Branches synced") || len(h.gl.CallsTo("PUT")) != 0 {
		t.Fatalf("report:\n%s\nPUTs: %v", r.err, h.gl.CallsTo("PUT"))
	}
}
