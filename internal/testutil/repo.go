// Package testutil builds throwaway git repositories for tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is a working clone with a bare "origin" next to it.
type Repo struct {
	t      *testing.T
	Dir    string
	Origin string
}

// NewRepo creates a bare origin with one commit on main and clones it.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	root := t.TempDir()
	r := &Repo{t: t, Dir: filepath.Join(root, "work"), Origin: filepath.Join(root, "origin.git")}
	isolateGitConfig(t, root)
	run(t, root, "git", "init", "--quiet", "--bare", "-b", "main", r.Origin)
	run(t, root, "git", "clone", "--quiet", r.Origin, r.Dir)
	r.Commit("README.md", "hello\n", "initial")
	r.Git("push", "--quiet", "-u", "origin", "main")
	r.Git("remote", "set-head", "origin", "main")
	return r
}

// isolateGitConfig keeps the user's global git config out of tests.
func isolateGitConfig(t *testing.T, root string) {
	t.Helper()
	cfg := filepath.Join(root, "gitconfig")
	content := "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n[advice]\n\tdetachedHead = false\n"
	if err := os.WriteFile(cfg, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TEMPLATE_DIR", "")
}

// Git runs git in the working clone and returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	return run(r.t, r.Dir, "git", args...)
}

// Commit writes file and commits it on the current branch.
func (r *Repo) Commit(file, content, msg string) string {
	r.t.Helper()
	path := filepath.Join(r.Dir, file)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.Git("add", file)
	r.Git("commit", "--quiet", "-m", msg)
	return r.Git("rev-parse", "HEAD")
}

func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

// PushFromElsewhere commits file on branch in a separate clone and pushes it,
// simulating someone else updating the remote.
func (r *Repo) PushFromElsewhere(branch, file, content, msg string) string {
	r.t.Helper()
	other := filepath.Join(r.t.TempDir(), "other")
	run(r.t, filepath.Dir(other), "git", "clone", "--quiet", "--branch", branch, r.Origin, other)
	o := &Repo{t: r.t, Dir: other, Origin: r.Origin}
	sha := o.Commit(file, content, msg)
	o.Git("push", "--quiet", "origin", branch)
	return sha
}

// Clone returns a fresh clone of the origin, e.g. to act as a teammate.
func (r *Repo) Clone() *Repo {
	r.t.Helper()
	other := filepath.Join(r.t.TempDir(), "clone")
	run(r.t, filepath.Dir(other), "git", "clone", "--quiet", r.Origin, other)
	return &Repo{t: r.t, Dir: other, Origin: r.Origin}
}
