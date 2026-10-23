package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

func TestInitCreatesBranches(t *testing.T) {
	h := newHarness(t)
	mainSHA := h.repo.Git("rev-parse", "main")
	h.must("init", "a", "b")

	if h.current() != "b" {
		t.Fatalf("current = %s, want b", h.current())
	}
	f := h.file()
	if len(f.Stacks) != 1 || !reflect.DeepEqual(names(f.Stacks[0]), []string{"a", "b"}) {
		t.Fatalf("stacks = %+v", f.Stacks)
	}
	s := f.Stacks[0]
	if s.Trunk != "main" || s.Remote != "origin" || s.Branches[0].Base != mainSHA || s.Branches[1].Base != mainSHA {
		t.Fatalf("stack = %+v", s)
	}
	if h.repo.Git("config", "rerere.enabled") != "true" {
		t.Fatal("rerere not enabled")
	}
}

func TestInitAdoptsExistingBranch(t *testing.T) {
	h := newHarness(t)
	mainSHA := h.repo.Git("rev-parse", "main")
	h.repo.Git("checkout", "--quiet", "-b", "feat")
	h.repo.Commit("f.txt", "f\n", "feat work")
	h.repo.Git("checkout", "--quiet", "main")
	h.repo.Commit("m.txt", "m\n", "trunk moved")

	h.must("init", "feat", "next")
	s := h.file().Stacks[0]
	if s.Branches[0].Base != mainSHA {
		t.Fatalf("adopted base = %s, want merge-base %s", s.Branches[0].Base, mainSHA)
	}
	if s.Branches[1].Base != h.repo.Git("rev-parse", "feat") {
		t.Fatal("next should start at feat's tip")
	}
}

func TestInitErrors(t *testing.T) {
	h := newHarness(t)
	if got := exitcode.Of(h.run("init").e); got != exitcode.InvalidArgs {
		t.Errorf("no args: exit %d", got)
	}
	if got := exitcode.Of(h.run("init", "main").e); got != exitcode.InvalidArgs {
		t.Errorf("trunk as layer: exit %d", got)
	}
	h.must("init", "a")
	if got := exitcode.Of(h.run("init", "a").e); got != exitcode.InvalidArgs {
		t.Errorf("already stacked: exit %d", got)
	}
	if got := exitcode.Of(h.run("init", "--base", "nope", "z").e); got != exitcode.InvalidArgs {
		t.Errorf("missing trunk: exit %d", got)
	}
}

func TestInitInteractive(t *testing.T) {
	h := newHarness(t)
	h.repo.Git("checkout", "--quiet", "-b", "mine")
	r := h.runIn(h.repo.Dir, "y\nsecond\n\n", true, "init")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); !reflect.DeepEqual(got, []string{"mine", "second"}) {
		t.Fatalf("layers = %v", got)
	}
}

func TestAddFromTop(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.must("add", "b")
	if h.current() != "b" {
		t.Fatalf("current = %s", h.current())
	}
	h.repo.Git("checkout", "--quiet", "a")
	if got := exitcode.Of(h.run("add", "c").e); got != exitcode.InvalidArgs {
		t.Fatalf("add from middle: exit %d", got)
	}
	h.repo.Git("checkout", "--quiet", "b")
	if got := exitcode.Of(h.run("add", "a").e); got != exitcode.InvalidArgs {
		t.Fatalf("duplicate: exit %d", got)
	}
}

func TestAddWithMessage(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "layer a")

	h.repo.Commit("seed.txt", "x\n", "seed") // something tracked to modify
	writeFile(t, h.repo.Dir+"/seed.txt", "changed\n")
	writeFile(t, h.repo.Dir+"/new.txt", "untracked\n")
	h.must("add", "-A", "-m", "Add login: v2!")

	if h.current() != "03-24-add_login_v2" {
		t.Fatalf("auto name = %s", h.current())
	}
	if h.repo.Git("log", "-1", "--format=%s") != "Add login: v2!" {
		t.Fatal("commit not created on new branch")
	}
	if h.repo.Git("status", "--porcelain") != "" {
		t.Fatal("-A left changes behind")
	}
	if got := names(h.file().Stacks[0]); len(got) != 2 {
		t.Fatalf("layers = %v", got)
	}
}

func TestAddCommitsOntoEmptyBranch(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	writeFile(t, h.repo.Dir+"/README.md", "edited\n")
	h.must("add", "-u", "-m", "first change")
	if h.current() != "a" {
		t.Fatalf("current = %s, want a", h.current())
	}
	if got := names(h.file().Stacks[0]); len(got) != 1 {
		t.Fatalf("layers = %v", got)
	}
	if !strings.Contains(h.repo.Git("log", "-1", "--format=%s"), "first change") {
		t.Fatal("commit missing")
	}
}

func TestAddFlagValidation(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	if got := exitcode.Of(h.run("add", "-A", "b").e); got != exitcode.InvalidArgs {
		t.Errorf("-A without -m: exit %d", got)
	}
	if got := exitcode.Of(h.run("add", "-A", "-u", "-m", "x").e); got != exitcode.InvalidArgs {
		t.Errorf("-A with -u: exit %d", got)
	}
	h.repo.Commit("a.txt", "a\n", "layer a")
	if got := exitcode.Of(h.run("add", "-m", "nothing staged").e); got != exitcode.InvalidArgs {
		t.Errorf("nothing staged: exit %d", got)
	}
	if h.repo.Git("branch", "--list", "03-24-nothing_staged") != "" {
		t.Error("branch created despite nothing to commit")
	}
}

func TestAddOutsideStack(t *testing.T) {
	h := newHarness(t)
	if got := exitcode.Of(h.run("add", "x").e); got != exitcode.NotInStack {
		t.Fatalf("non-interactive: exit %d", got)
	}
	r := h.runIn(h.repo.Dir, "y\n", true, "add", "x")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	s := h.file().Stacks[0]
	if s.Trunk != "main" || !reflect.DeepEqual(names(s), []string{"x"}) || h.current() != "x" {
		t.Fatalf("stack = %+v, current %s", s, h.current())
	}
}

func TestInvalidBranchNameCreatesNothing(t *testing.T) {
	h := newHarness(t)
	if got := exitcode.Of(h.run("init", "ok", "b c").e); got != exitcode.InvalidArgs {
		t.Fatalf("init: exit %d", got)
	}
	if h.repo.Git("branch", "--list", "ok") != "" {
		t.Fatal("init left branch ok behind")
	}
	h.must("init", "a")
	if got := exitcode.Of(h.run("add", "bad..name").e); got != exitcode.InvalidArgs {
		t.Fatalf("add: exit %d", got)
	}
}

// add offers to start a stack while the trunk exists only as origin/main.
func TestAddOfferInitWithoutLocalTrunk(t *testing.T) {
	h := newHarness(t)
	h.repo.Git("checkout", "--quiet", "-b", "feature")
	h.repo.Commit("f.txt", "f\n", "f1")
	h.repo.Git("branch", "-D", "main")
	if r := h.runIn(h.repo.Dir, "y\n", true, "add", "next"); r.e != nil {
		t.Fatal(r.e, r.err)
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "feature,next" {
		t.Fatalf("stack = %v", got)
	}
}

// A commit rejected by a hook still leaves the new layer in the stack.
func TestAddKeepsLayerWhenCommitFails(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")
	hook := filepath.Join(h.repo.Dir, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho lint failed >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, h.repo.Dir+"/b.txt", "b\n")
	if r := h.run("add", "-A", "-m", "second layer", "b"); r.e == nil {
		t.Fatal("add succeeded although the commit failed")
	}
	if got := names(h.file().Stacks[0]); strings.Join(got, ",") != "a,b" || h.current() != "b" {
		t.Fatalf("stack = %v, on %s", got, h.current())
	}
}

// A branch that exists only on the remote is adopted, not recreated.
func TestInitAdoptsRemoteOnlyBranch(t *testing.T) {
	h := newHarness(t)
	other := h.repo.Clone()
	other.Git("checkout", "--quiet", "-b", "feat", "origin/main")
	feat := other.Commit("feat.txt", "feat\n", "teammate's feat")
	other.Git("push", "--quiet", "origin", "feat")
	h.repo.Git("fetch", "--quiet")

	h.must("init", "feat")
	if h.sha("feat") != feat {
		t.Fatalf("feat = %s, want the remote's %s", h.sha("feat"), feat)
	}
}
