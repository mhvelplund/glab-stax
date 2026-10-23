package cli

import (
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
	"github.com/mhvelplund/glab-stax/internal/testutil"
)

// teammate returns a fresh clone sharing h's fake GitLab.
func (h *harness) teammate() *testutil.Repo { return h.repo.Clone() }

func (h *harness) in(r *testutil.Repo, stdin string, interactive bool, args ...string) result {
	h.t.Helper()
	return h.runIn(r.Dir, stdin, interactive, args...)
}

func stacksIn(t *testing.T, r *testutil.Repo) *stack.File {
	t.Helper()
	f, err := stack.Store{Dir: r.Dir + "/.git"}.Load()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCheckoutRemoteStackByMR(t *testing.T) {
	h := submitted(t)
	tm := h.teammate()

	for _, arg := range []string{"2", "!2", "https://gitlab.example/group/repo/-/merge_requests/2"} {
		r := h.in(tm, "", false, "checkout", arg)
		if r.e != nil {
			t.Fatalf("checkout %s: %v\n%s", arg, r.e, r.err)
		}
		if cur := tm.Git("branch", "--show-current"); cur != "b" {
			t.Fatalf("checkout %s: on %s", arg, cur)
		}
	}
	f := stacksIn(t, tm)
	if len(f.Stacks) != 1 || len(f.Stacks[0].Branches) != 3 || f.Stacks[0].Branches[2].MR.IID != 3 {
		t.Fatalf("stacks = %+v", f.Stacks)
	}
	if tm.Git("rev-parse", "c") != h.sha("c") {
		t.Fatal("c not created from the remote")
	}
	// The set-up stack is fully usable.
	h.in(tm, "", false, "top")
	if tm.Git("branch", "--show-current") != "c" {
		t.Fatal("navigation broken in checked-out stack")
	}
}

func TestCheckoutByBranchName(t *testing.T) {
	h := submitted(t)
	tm := h.teammate()
	if r := h.in(tm, "", false, "checkout", "c"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if tm.Git("branch", "--show-current") != "c" || len(stacksIn(t, tm).Stacks) != 1 {
		t.Fatal("stack not set up from branch name")
	}
	if got := exitcode.Of(h.in(tm, "", false, "checkout", "nope").e); got != exitcode.NotInStack {
		t.Fatalf("unknown branch: exit %d", got)
	}
}

func TestCheckoutInfersStackWithoutBlock(t *testing.T) {
	h := threeLayers(t)
	h.must("push")
	h.gl.Add(glab.MR{IID: 1, SourceBranch: "a", TargetBranch: "main"})
	h.gl.Add(glab.MR{IID: 2, SourceBranch: "b", TargetBranch: "a"})
	h.gl.Add(glab.MR{IID: 3, SourceBranch: "c", TargetBranch: "b"})
	tm := h.teammate()
	if r := h.in(tm, "", false, "checkout", "2"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	s := stacksIn(t, tm).Stacks[0]
	if got := names(s); len(got) != 3 || got[0] != "a" || got[2] != "c" || s.Trunk != "main" {
		t.Fatalf("inferred %v on %s", got, s.Trunk)
	}
}

func TestCheckoutLocalAndPicker(t *testing.T) {
	h := submitted(t)
	h.must("checkout", "1")
	if h.current() != "a" {
		t.Fatalf("on %s", h.current())
	}
	if got := exitcode.Of(h.run("checkout").e); got != exitcode.InvalidArgs {
		t.Fatalf("no args non-interactive: exit %d", got)
	}
	tm := h.teammate()
	h.keys = []string{"tab", "tab", "enter"} // Remote tab, first stack
	r := h.in(tm, "", true, "checkout")
	if r.e != nil {
		t.Fatalf("%v\n%s", r.e, r.err)
	}
	if tm.Git("branch", "--show-current") != "c" {
		t.Fatal("picker did not check out the stack's top")
	}
}

func TestUnstack(t *testing.T) {
	h := submitted(t)
	h.must("unstack", "--local")
	if len(h.file().Stacks) != 0 {
		t.Fatal("local tracking kept")
	}
	if _, ok := glab.ParseBlock(h.gl.MRs[1].Description); !ok {
		t.Fatal("--local touched GitLab")
	}

	// From a clone that never tracked it, by MR number.
	tm := h.teammate()
	if r := h.in(tm, "", false, "delete", "2"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	for iid, mr := range h.gl.MRs {
		if _, ok := glab.ParseBlock(mr.Description); ok {
			t.Errorf("!%d still has a block", iid)
		}
	}
}

func TestUnstackCurrent(t *testing.T) {
	h := submitted(t)
	h.must("unstack")
	if len(h.file().Stacks) != 0 || strings.Contains(h.gl.MRs[3].Description, glab.Marker) {
		t.Fatal("unstack incomplete")
	}
}

func TestLinkCreatesAndExtends(t *testing.T) {
	h := newHarness(t)
	h.repo.Git("checkout", "--quiet", "-b", "x")
	h.repo.Commit("x.txt", "x\n", "x1")
	h.repo.Git("checkout", "--quiet", "-b", "y")
	h.repo.Commit("y.txt", "y\n", "y1")

	h.must("link", "x", "y")
	if h.gl.MRs[1].TargetBranch != "main" || h.gl.MRs[2].TargetBranch != "x" {
		t.Fatalf("targets: %s, %s", h.gl.MRs[1].TargetBranch, h.gl.MRs[2].TargetBranch)
	}
	if len(h.file().Stacks) != 0 {
		t.Fatal("link changed local tracking")
	}

	// Insert z between x and y, by MR number for x: additive, order kept.
	h.repo.Git("checkout", "--quiet", "-b", "z", "x")
	h.repo.Commit("z.txt", "z\n", "z1")
	h.must("link", "1", "z")
	info, _ := glab.ParseBlock(h.gl.MRs[2].Description)
	var order []string
	for _, m := range info.MRs {
		order = append(order, m.Branch)
	}
	if strings.Join(order, ",") != "x,z,y" {
		t.Fatalf("order = %v", order)
	}
	if h.gl.MRs[3].TargetBranch != "x" {
		t.Fatalf("z targets %s", h.gl.MRs[3].TargetBranch)
	}
	if got := exitcode.Of(h.run("link", "no-such").e); got != exitcode.InvalidArgs {
		t.Fatalf("bad arg: exit %d", got)
	}
}

func TestMergeRemoteOnlyStack(t *testing.T) {
	h := mergeable(t)
	tm := h.teammate()
	if r := h.in(tm, "", false, "merge", "1", "--yes", "--squash"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if !h.gl.MRs[1].Merged() || h.gl.MRs[2].Merged() {
		t.Fatal("wrong MRs merged")
	}
	if tm.Git("branch", "--show-current") != "main" {
		t.Fatal("merge switched branches")
	}
}

func TestSwitch(t *testing.T) {
	h := threeLayers(t)
	if got := exitcode.Of(h.run("switch").e); got != exitcode.InvalidArgs {
		t.Fatalf("non-interactive: exit %d", got)
	}
	// List: 3. c (current), 2. b, 1. a, main. Two down lands on a.
	h.keys = []string{"j", "j", "enter"}
	if r := h.runIn(h.repo.Dir, "", true, "switch"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if h.current() != "a" {
		t.Fatalf("on %s", h.current())
	}
	h.keys = []string{"/", "m", "a", "i", "enter", "enter"}
	h.runIn(h.repo.Dir, "", true, "switch")
	if h.current() != "main" {
		t.Fatalf("search: on %s", h.current())
	}
	h.keys = []string{"q"}
	h.runIn(h.repo.Dir, "", true, "switch")
	if h.current() != "main" {
		t.Fatal("cancel switched branches")
	}
}

// Inferring a stack from target branches stops at a protected branch: an
// open main -> production MR is not a layer.
func TestCheckoutInferenceStopsAtProtectedBranch(t *testing.T) {
	h := newHarness(t)
	h.repo.Git("checkout", "--quiet", "-b", "feature")
	h.repo.Commit("f.txt", "f\n", "f1")
	h.repo.Git("push", "--quiet", "origin", "feature")
	h.repo.Git("checkout", "--quiet", "main")
	h.gl.Add(glab.MR{IID: 1, Title: "release", SourceBranch: "main", TargetBranch: "production"})
	h.gl.Add(glab.MR{IID: 2, Title: "f1", SourceBranch: "feature", TargetBranch: "main"})
	h.must("checkout", "2")
	s := h.file().Stacks[0]
	if s.Trunk != "main" || strings.Join(names(s), ",") != "feature" {
		t.Fatalf("stack on %s: %v", s.Trunk, names(s))
	}
}

// An MR URL of another project is refused rather than applied to this one.
func TestMRURLOfOtherProject(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	for _, args := range [][]string{
		{"checkout", "https://gitlab.example/other/project/-/merge_requests/2"},
		{"merge", "https://gitlab.example/other/project/-/merge_requests/2", "--yes"},
		{"unstack", "https://gitlab.example/other/project/-/merge_requests/2"},
	} {
		if r := h.run(args...); exitcode.Of(r.e) != exitcode.InvalidArgs {
			t.Errorf("%v: exit %d: %v", args, exitcode.Of(r.e), r.e)
		}
	}
	if h.run("checkout", "https://gitlab.example/group/repo/-/merge_requests/2").e != nil {
		t.Error("this project's MR URL was refused")
	}
}

// Extending a stack from its top keeps the top MR's target.
func TestLinkExtendsFromTop(t *testing.T) {
	h := newHarness(t)
	h.repo.Git("checkout", "--quiet", "-b", "x")
	h.repo.Commit("x.txt", "x\n", "x1")
	h.repo.Git("checkout", "--quiet", "-b", "y")
	h.repo.Commit("y.txt", "y\n", "y1")
	h.must("link", "x", "y")
	h.repo.Git("checkout", "--quiet", "-b", "w")
	h.repo.Commit("w.txt", "w\n", "w1")
	h.must("link", "2", "w")
	if h.gl.MRs[2].TargetBranch != "x" || h.gl.MRs[3].TargetBranch != "y" {
		t.Fatalf("targets: !2 -> %s, !3 -> %s", h.gl.MRs[2].TargetBranch, h.gl.MRs[3].TargetBranch)
	}
	info, _ := glab.ParseBlock(h.gl.MRs[1].Description)
	var order []string
	for _, m := range info.MRs {
		order = append(order, m.Branch)
	}
	if strings.Join(order, ",") != "x,y,w" || info.Trunk != "main" {
		t.Fatalf("block = %+v", info)
	}
}

// A teammate checks out a stack whose bottom MR was squash-merged and its
// branch deleted: the next layer still gets a cut point, so restacks work.
func TestCheckoutStackWithDeletedMergedBottom(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	h.gl.MRs[1].State, h.gl.MRs[1].SHA = "merged", h.sha("a")
	h.repo.PushFromElsewhere("main", "a.txt", "a\n", "a1 (squashed !1)")
	h.repo.Git("push", "--quiet", "origin", "--delete", "a")
	tm := h.teammate()
	if r := h.in(tm, "", false, "checkout", "3"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if b := stacksIn(t, tm).Stacks[0].Get("b"); b.Base != h.sha("a") {
		t.Fatalf("b's base = %q, want a's last commit", b.Base)
	}
	h.repo.PushFromElsewhere("main", "up.txt", "up\n", "upstream")
	if r := h.in(tm, "", false, "sync"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := tm.Git("rev-list", "--count", "origin/main..b"); got != "2" {
		t.Fatalf("b has %s commits over the trunk, want 2", got)
	}
}
