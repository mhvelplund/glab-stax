package cli

import (
	"encoding/json"
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

// GitLab reports "locked" while it performs a merge; that is not a failure.
func TestMergeWaitsThroughLockedState(t *testing.T) {
	h := mergeable(t)
	h.gl.PendingPolls = 1
	merged, locked := false, false
	h.runner = func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		out, err := h.gl.Run(args...)
		if strings.Contains(joined, "-X PUT projects/group%2Frepo/merge_requests/1/merge ") {
			merged = true
		} else if merged && !locked && err == nil && strings.HasSuffix(joined, "-X GET projects/group%2Frepo/merge_requests/1") {
			locked = true
			var m map[string]any
			_ = json.Unmarshal(out, &m)
			m["state"] = "locked"
			return json.Marshal(m)
		}
		return out, err
	}
	if r := h.run("merge", "1", "--yes", "--squash"); r.e != nil {
		t.Fatalf("merge failed on a transient locked state: %v\n%s", r.e, r.err)
	}
	if !locked || !h.gl.MRs[1].Merged() {
		t.Fatal("the locked state was not exercised")
	}
}

// !1 was merged earlier (here on GitLab) while !2 still targets a: !2 must be
// restacked and retargeted before it is merged, not merged into a.
func TestMergeRetargetsFirstLayerStillOnMergedBranch(t *testing.T) {
	h := mergeable(t)
	if err := h.gl.OnMerge(h.gl.MRs[1], true); err != nil {
		t.Fatal(err)
	}
	h.gl.MRs[1].State = "merged"
	onMerge := h.gl.OnMerge
	targets := map[int]string{}
	h.gl.OnMerge = func(mr *glab.MR, squash bool) error {
		targets[mr.IID] = mr.TargetBranch
		return onMerge(mr, squash)
	}
	h.must("merge", "--yes", "--squash")
	if targets[2] != "main" || targets[3] != "main" {
		t.Fatalf("targets at merge time: %v", targets)
	}
}

// An MR that is in no stack block is not merged together with whatever its
// target branches lead to.
func TestMergeRefusesMRWithoutStackBlock(t *testing.T) {
	h := newHarness(t)
	h.gl.Add(glab.MR{IID: 1, Title: "release", SourceBranch: "main", TargetBranch: "production"})
	h.gl.Add(glab.MR{IID: 2, Title: "feature", SourceBranch: "feature", TargetBranch: "main"})
	r := h.run("merge", "2", "--yes", "--squash")
	if exitcode.Of(r.e) != exitcode.NotInStack {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	for _, c := range h.gl.Calls {
		if strings.HasSuffix(c.Endpoint, "/merge") {
			t.Fatal("tried to merge")
		}
	}
}

// Failing to reach GitLab is a GitLab failure, not "not in a stack".
func TestMergeLookupFailureExitCode(t *testing.T) {
	h := mergeable(t)
	h.runner = func(args ...string) ([]byte, error) { return nil, errors.New("401 Unauthorized") }
	if got := exitcode.Of(h.run("merge", "99", "--yes").e); got != exitcode.GitLabFailure {
		t.Fatalf("exit %d", got)
	}
}
