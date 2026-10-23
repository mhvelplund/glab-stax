package git_test

import (
	"path/filepath"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/testutil"
)

func TestBasics(t *testing.T) {
	tr := testutil.NewRepo(t)
	g := &git.Repo{Dir: tr.Dir}

	if b, err := g.CurrentBranch(); err != nil || b != "main" {
		t.Fatalf("CurrentBranch = %q, %v", b, err)
	}
	if rm, err := g.DefaultRemote(); err != nil || rm != "origin" {
		t.Fatalf("DefaultRemote = %q, %v", rm, err)
	}
	if db, err := g.DefaultBranch("origin"); err != nil || db != "main" {
		t.Fatalf("DefaultBranch = %q, %v", db, err)
	}
	cd, err := g.CommonDir()
	if err != nil || cd != filepath.Join(tr.Dir, ".git") {
		t.Fatalf("CommonDir = %q, %v", cd, err)
	}

	base := tr.Git("rev-parse", "HEAD")
	if err := g.CreateBranch("feat", "main"); err != nil {
		t.Fatal(err)
	}
	if !g.BranchExists("feat") || g.BranchExists("nope") {
		t.Fatal("BranchExists wrong")
	}
	if err := g.Checkout("feat"); err != nil {
		t.Fatal(err)
	}
	tr.Commit("a.txt", "a\n", "a")
	head := tr.Commit("b.txt", "b\n", "b")
	if n, err := g.CommitCount("main", "feat"); err != nil || n != 2 {
		t.Fatalf("CommitCount = %d, %v", n, err)
	}
	if !g.IsAncestor(base, head) || g.IsAncestor(head, base) {
		t.Fatal("IsAncestor wrong")
	}
	if sha, err := g.RevParse("feat"); err != nil || sha != head {
		t.Fatalf("RevParse = %q, %v", sha, err)
	}
	if clean, err := g.IsClean(); err != nil || !clean {
		t.Fatalf("IsClean = %v, %v", clean, err)
	}
	if err := g.SetBranchSHA("main", head); err != nil {
		t.Fatal(err)
	}
	if sha, _ := g.RevParse("main"); sha != head {
		t.Fatal("SetBranchSHA did not move main")
	}

	tr.Git("checkout", "--quiet", "--detach")
	if b, err := g.CurrentBranch(); err != nil || b != "" {
		t.Fatalf("detached CurrentBranch = %q, %v", b, err)
	}
}

func TestWorktrees(t *testing.T) {
	tr := testutil.NewRepo(t)
	g := &git.Repo{Dir: tr.Dir}
	other := filepath.Join(t.TempDir(), "wt")
	tr.Git("worktree", "add", "--quiet", "-b", "side", other)

	path, err := g.WorktreeFor("side")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(other)
	got, _ := filepath.EvalSymlinks(path)
	if got != want {
		t.Fatalf("WorktreeFor = %q, want %q", got, want)
	}
	// The linked worktree shares the main common dir.
	cd, err := g.At(other).CommonDir()
	if err != nil {
		t.Fatal(err)
	}
	wantCD, _ := filepath.EvalSymlinks(filepath.Join(tr.Dir, ".git"))
	gotCD, _ := filepath.EvalSymlinks(cd)
	if gotCD != wantCD {
		t.Fatalf("linked CommonDir = %q, want %q", gotCD, wantCD)
	}
}

func TestConflictDetection(t *testing.T) {
	tr := testutil.NewRepo(t)
	g := &git.Repo{Dir: tr.Dir}
	tr.Git("checkout", "--quiet", "-b", "feat")
	tr.Commit("README.md", "feat\n", "feat")
	tr.Git("checkout", "--quiet", "main")
	tr.Commit("README.md", "main\n", "main")
	if _, err := g.Run("rebase", "main", "feat"); err == nil {
		t.Fatal("expected conflict")
	}
	if in, _ := g.RebaseInProgress(); !in {
		t.Fatal("RebaseInProgress = false")
	}
	files, err := g.ConflictedFiles()
	if err != nil || len(files) != 1 || files[0] != "README.md" {
		t.Fatalf("ConflictedFiles = %v, %v", files, err)
	}
}
