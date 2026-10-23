package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
)

// mergeable returns three layers with ready MRs whose merges land on the remote.
func mergeable(t *testing.T) *harness {
	h := threeLayers(t)
	h.must("submit", "--auto", "--open")
	h.gl.OnMerge = func(mr *glab.MR, squash bool) error {
		o := h.repo.Clone()
		if squash {
			o.Git("merge", "--quiet", "--squash", "origin/"+mr.SourceBranch)
			o.Git("commit", "--quiet", "-m", mr.Title)
		} else {
			o.Git("merge", "--quiet", "--no-ff", "-m", "Merge "+mr.SourceBranch, "origin/"+mr.SourceBranch)
		}
		o.Git("push", "--quiet", "origin", "main")
		return nil
	}
	return h
}

func (h *harness) originMainFiles() string {
	return h.repo.Git("ls-tree", "--name-only", "origin/main")
}

func TestMergeWholeStackSquash(t *testing.T) {
	h := mergeable(t)
	h.must("merge", "--yes", "--squash")

	for iid := 1; iid <= 3; iid++ {
		if !h.gl.MRs[iid].Merged() {
			t.Fatalf("!%d not merged", iid)
		}
	}
	h.repo.Git("fetch", "--quiet")
	files := h.originMainFiles()
	for _, f := range []string{"a.txt", "b.txt", "b2.txt", "c.txt"} {
		if !strings.Contains(files, f) {
			t.Errorf("origin/main lacks %s", f)
		}
	}
	if n := h.count("main~3", "origin/main"); n != "3" {
		t.Errorf("expected 3 squash commits, got %s", n)
	}
	if h.gl.MRs[3].TargetBranch != "main" {
		t.Errorf("!3 merged while targeting %s", h.gl.MRs[3].TargetBranch)
	}
	for _, b := range h.file().Stacks[0].Branches {
		if !b.Merged {
			t.Errorf("%s not marked merged", b.Name)
		}
	}
	if h.sha("main") != h.sha("origin/main") {
		t.Error("local main not fast-forwarded")
	}
	if got, _ := h.runGit("config", mergeMethodKey); got != "squash" {
		t.Errorf("merge method not remembered: %q", got)
	}
}

func TestMergeUpToMR(t *testing.T) {
	h := mergeable(t)
	h.must("merge", "2", "--yes", "--squash")

	if !h.gl.MRs[1].Merged() || !h.gl.MRs[2].Merged() || h.gl.MRs[3].Merged() {
		t.Fatal("wrong MRs merged")
	}
	if h.gl.MRs[3].TargetBranch != "main" {
		t.Fatalf("!3 targets %s", h.gl.MRs[3].TargetBranch)
	}
	h.assertStacked("main", "c", "1")
	if !strings.HasPrefix(h.remoteSHA("c"), h.sha("c")) {
		t.Fatal("restacked c not pushed")
	}
}

func TestMergeKeepsCommits(t *testing.T) {
	h := mergeable(t)
	h.must("merge", "--yes", "--merge")
	h.repo.Git("fetch", "--quiet")
	if !strings.Contains(h.repo.Git("log", "--format=%s", "origin/main"), "Merge c") {
		t.Fatal("no merge commit for c")
	}
}

func TestMergeRefusesDrafts(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	r := h.run("merge", "--yes")
	if r.e == nil || !strings.Contains(r.e.Error(), "draft") {
		t.Fatalf("err = %v", r.e)
	}
	for _, c := range h.gl.Calls {
		if strings.HasSuffix(c.Endpoint, "/merge") {
			t.Fatal("tried to merge")
		}
	}
}

func TestMergeStopsOnFailure(t *testing.T) {
	h := mergeable(t)
	h.gl.Fail["PUT projects/:project/merge_requests/2/merge"] = errors.New("405 Method Not Allowed")
	r := h.run("merge", "--yes", "--squash")
	if exitcode.Of(r.e) != exitcode.GitLabFailure || !strings.Contains(r.err, "Merged so far: !1") {
		t.Fatalf("exit %d: %v\n%s", exitcode.Of(r.e), r.e, r.err)
	}
	s := h.file().Stacks[0]
	if !s.Branches[0].Merged || s.Branches[1].Merged {
		t.Fatal("metadata does not reflect the partial merge")
	}
}

func TestMergeWaitsForPipeline(t *testing.T) {
	h := mergeable(t)
	h.gl.PendingPolls = 2
	h.must("merge", "--yes", "--squash")
	if !h.gl.MRs[3].Merged() || h.slept < 2*pollInterval {
		t.Fatalf("merged %v, slept %v", h.gl.MRs[3].Merged(), h.slept)
	}
}

func TestMergeTimesOut(t *testing.T) {
	h := mergeable(t)
	h.gl.PendingPolls = 1000
	h.tick = time.Minute
	r := h.run("merge", "--yes", "--squash", "--timeout", "10m")
	if exitcode.Of(r.e) != exitcode.GitLabFailure || !strings.Contains(r.e.Error(), "timed out") {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
}

func TestMergeDeletesSourceAfterRetarget(t *testing.T) {
	h := mergeable(t)
	h.gl.MRs[1].RemoveSource = true
	h.must("merge", "2", "--yes", "--squash")
	if h.remoteSHA("a") != "" {
		t.Fatal("a was not deleted on the remote")
	}
	if h.remoteSHA("b") == "" {
		t.Fatal("b was deleted although its MR did not ask for it")
	}
	if h.gl.MRs[2].State != "merged" {
		t.Fatalf("!2 is %s", h.gl.MRs[2].State)
	}
}

func TestMergeWizard(t *testing.T) {
	h := mergeable(t)
	// Options are listed top-down: 1) !3  2) !2  3) !1. Then the method list
	// starts with the default (merge): 1) merge 2) squash 3) rebase.
	r := h.runIn(h.repo.Dir, "2\n2\ny\n", true, "merge")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !h.gl.MRs[2].Merged() || h.gl.MRs[3].Merged() {
		t.Fatal("wizard selection ignored")
	}
	if got, _ := h.runGit("config", mergeMethodKey); got != "squash" {
		t.Fatalf("method = %q", got)
	}
}

func TestMergeArgErrors(t *testing.T) {
	h := mergeable(t)
	if got := exitcode.Of(h.run("merge", "--squash", "--merge").e); got != exitcode.InvalidArgs {
		t.Errorf("two methods: exit %d", got)
	}
	if got := exitcode.Of(h.run("merge", "--merge-method", "fast").e); got != exitcode.InvalidArgs {
		t.Errorf("bad method: exit %d", got)
	}
	if got := exitcode.Of(h.run("merge", "99", "--yes").e); got != exitcode.NotInStack {
		t.Errorf("unknown MR: exit %d", got)
	}
}

func (h *harness) runGit(args ...string) (string, error) {
	return (&App{Dir: h.repo.Dir}).repo().Run(args...)
}
