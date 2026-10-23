package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// threeLayers builds a, b, c with one commit each and c checked out.
func threeLayers(t *testing.T) *harness {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")
	h.must("add", "b")
	h.repo.Commit("b.txt", "b\n", "b1")
	h.repo.Commit("b2.txt", "b\n", "b2")
	h.must("add", "c")
	h.repo.Commit("c.txt", "c\n", "c1")
	return h
}

func TestViewJSON(t *testing.T) {
	h := threeLayers(t)
	var d viewData
	if err := json.Unmarshal([]byte(h.must("view", "--json").out), &d); err != nil {
		t.Fatal(err)
	}
	if d.Trunk != "main" || d.Current != "c" || len(d.Branches) != 3 {
		t.Fatalf("view = %+v", d)
	}
	b := d.Branches[1]
	if b.Name != "b" || b.Parent != "a" || b.Commits != 2 || b.Subject != "b2" || b.IsCurrent {
		t.Fatalf("branch b = %+v", b)
	}
}

func TestViewShortAndLong(t *testing.T) {
	h := threeLayers(t)
	err := mutateFile(h, func(f *stack.File) {
		f.Stacks[0].Branches[0].MR = &stack.MR{IID: 12, URL: "https://gl/x/-/merge_requests/12"}
	})
	if err != nil {
		t.Fatal(err)
	}
	short := h.must("view", "--short").out
	want := "● c\n○ b\n○ a !12\n  main\n"
	if short != want {
		t.Fatalf("short =\n%q\nwant\n%q", short, want)
	}
	long := h.must("view").out
	for _, s := range []string{"Stack on main (3 branches)", "● c (current)", "!12 https://gl/x/-/merge_requests/12", "2 commits", "└ main"} {
		if !strings.Contains(long, s) {
			t.Errorf("long view missing %q:\n%s", s, long)
		}
	}
}

func TestViewOutsideStack(t *testing.T) {
	h := newHarness(t)
	if got := exitcode.Of(h.run("view").e); got != exitcode.NotInStack {
		t.Fatalf("exit %d", got)
	}
}

func TestNavigation(t *testing.T) {
	h := threeLayers(t)
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"down"}, "b"},
		{[]string{"down", "5"}, "a"},
		{[]string{"down"}, "a"},
		{[]string{"top"}, "c"},
		{[]string{"up"}, "c"},
		{[]string{"bottom"}, "a"},
		{[]string{"up", "2"}, "c"},
		{[]string{"trunk"}, "main"},
		{[]string{"up"}, "a"},
	}
	for _, s := range steps {
		h.must(s.args...)
		if got := h.current(); got != s.want {
			t.Fatalf("%v: on %s, want %s", s.args, got, s.want)
		}
	}
	if got := exitcode.Of(h.run("up", "x").e); got != exitcode.InvalidArgs {
		t.Fatalf("bad n: exit %d", got)
	}
}

func TestNavigationSkipsMerged(t *testing.T) {
	h := threeLayers(t)
	if err := mutateFile(h, func(f *stack.File) { f.Stacks[0].Branches[1].Merged = true }); err != nil {
		t.Fatal(err)
	}
	h.must("down")
	if h.current() != "a" {
		t.Fatalf("on %s, want a", h.current())
	}
	h.must("up")
	if h.current() != "c" {
		t.Fatalf("on %s, want c", h.current())
	}
}

func TestPrintPathAndWorktrees(t *testing.T) {
	h := threeLayers(t)
	other := filepath.Join(t.TempDir(), "wt-a")
	h.repo.Git("worktree", "add", "--quiet", other, "a")

	if got := exitcode.Of(h.run("bottom").e); got != exitcode.Generic {
		t.Fatalf("bottom into foreign worktree: exit %d", got)
	}
	r := h.must("bottom", "--print-path")
	if !samePath(strings.TrimSpace(r.out), other) || h.current() != "c" {
		t.Fatalf("print-path = %q, current %s", r.out, h.current())
	}
	r = h.must("down", "--print-path")
	if !samePath(strings.TrimSpace(r.out), h.repo.Dir) || h.current() != "b" {
		t.Fatalf("print-path = %q, current %s", r.out, h.current())
	}
}

func mutateFile(h *harness, fn func(f *stack.File)) error {
	st := stack.Store{Dir: h.repo.Dir + "/.git"}
	f, err := st.Load()
	if err != nil {
		return err
	}
	fn(f)
	return st.Save(f)
}
