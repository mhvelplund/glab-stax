// Package git is a thin wrapper around the git binary.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repo runs git commands in Dir.
type Repo struct {
	Dir string
}

// Error is returned when git exits non-zero.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: %s", strings.Join(e.Args, " "), msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Run executes git with args and returns trimmed stdout.
func (r *Repo) Run(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// At returns a Repo rooted at dir, e.g. another worktree.
func (r *Repo) At(dir string) *Repo { return &Repo{Dir: dir} }

// TopLevel returns the root of the current worktree.
func (r *Repo) TopLevel() (string, error) { return r.Run("rev-parse", "--show-toplevel") }

// CommonDir returns the absolute git common dir, shared by all worktrees.
func (r *Repo) CommonDir() (string, error) {
	out, err := r.Run("rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(r.Dir, out)
	}
	return filepath.Clean(out), nil
}

// GitDir returns the absolute per-worktree git dir.
func (r *Repo) GitDir() (string, error) { return r.Run("rev-parse", "--absolute-git-dir") }

// CurrentBranch returns the checked-out branch, or "" when HEAD is detached.
// It strips refs/heads/ itself: `--short` yields "heads/x" when a tag x exists.
func (r *Repo) CurrentBranch() (string, error) {
	out, err := r.Run("symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) {
			return "", nil // detached HEAD
		}
		return "", err
	}
	return strings.TrimPrefix(out, "refs/heads/"), nil
}

// RevParse resolves rev to a full SHA.
func (r *Repo) RevParse(rev string) (string, error) {
	return r.Run("rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// Ref returns the full ref of local branch name. Passing it instead of the
// bare name keeps a tag of the same name from being picked.
func Ref(name string) string { return "refs/heads/" + name }

// BranchExists reports whether a local branch exists.
func (r *Repo) BranchExists(name string) bool {
	_, err := r.Run("show-ref", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// CreateBranch creates name at start without checking it out.
func (r *Repo) CreateBranch(name, start string) error {
	_, err := r.Run("branch", name, start)
	return err
}

// Checkout switches to branch. The "--" keeps git from taking a missing
// branch's name as a path and restoring that path from the index instead.
func (r *Repo) Checkout(branch string) error {
	_, err := r.Run("checkout", "--quiet", branch, "--")
	return err
}

// SetBranchSHA force-moves a branch that is not checked out.
func (r *Repo) SetBranchSHA(branch, sha string) error {
	_, err := r.Run("update-ref", "refs/heads/"+branch, sha)
	return err
}

// IsClean reports whether the worktree has no staged or unstaged changes to tracked files.
func (r *Repo) IsClean() (bool, error) {
	out, err := r.Run("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// IsAncestor reports whether a is an ancestor of b.
func (r *Repo) IsAncestor(a, b string) bool {
	_, err := r.Run("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// MergeBase returns the best common ancestor of a and b.
func (r *Repo) MergeBase(a, b string) (string, error) { return r.Run("merge-base", a, b) }

// CommitCount returns the number of commits in base..head.
func (r *Repo) CommitCount(base, head string) (int, error) {
	out, err := r.Run("rev-list", "--count", base+".."+head)
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscanf(out, "%d", &n)
	return n, err
}

// Fetch fetches from remote.
func (r *Repo) Fetch(remote string) error {
	_, err := r.Run("fetch", "--quiet", "--prune", remote)
	return err
}

// Remotes lists configured remotes.
func (r *Repo) Remotes() ([]string, error) {
	out, err := r.Run("remote")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// DefaultRemote returns "origin" when present, else the only remote.
func (r *Repo) DefaultRemote() (string, error) {
	remotes, err := r.Remotes()
	if err != nil {
		return "", err
	}
	for _, rm := range remotes {
		if rm == "origin" {
			return rm, nil
		}
	}
	if len(remotes) == 1 {
		return remotes[0], nil
	}
	if len(remotes) == 0 {
		return "", fmt.Errorf("no git remote configured")
	}
	return "", fmt.Errorf("multiple remotes and no origin; pass --remote")
}

// DefaultBranch returns the remote's default branch from refs/remotes/<remote>/HEAD,
// falling back to main or master.
func (r *Repo) DefaultBranch(remote string) (string, error) {
	if out, err := r.Run("symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		return strings.TrimPrefix(out, remote+"/"), nil
	}
	for _, b := range []string{"main", "master"} {
		if r.BranchExists(b) {
			return b, nil
		}
	}
	return "", fmt.Errorf("cannot determine default branch; pass --base")
}

// EnableRerere turns on rerere for this repository.
func (r *Repo) EnableRerere() error {
	_, err := r.Run("config", "rerere.enabled", "true")
	return err
}

// Worktree describes one entry of `git worktree list`.
type Worktree struct {
	Path   string
	Branch string // short name; "" when detached
}

// Worktrees lists all worktrees of the repository.
func (r *Repo) Worktrees() ([]Worktree, error) {
	out, err := r.Run("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case cur != nil && strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return list, nil
}

// WorktreeFor returns the path of the worktree that has branch checked out, or "".
func (r *Repo) WorktreeFor(branch string) (string, error) {
	wts, err := r.Worktrees()
	if err != nil {
		return "", err
	}
	for _, wt := range wts {
		if wt.Branch == branch {
			return wt.Path, nil
		}
	}
	return "", nil
}

// RebaseInProgress reports whether this worktree is mid-rebase.
func (r *Repo) RebaseInProgress() (bool, error) {
	dir, err := r.GitDir()
	if err != nil {
		return false, err
	}
	for _, d := range []string{"rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(dir, d)); err == nil {
			return true, nil
		}
	}
	return false, nil
}

// ConflictedFiles lists paths with unresolved conflicts.
func (r *Repo) ConflictedFiles() ([]string, error) {
	out, err := r.Run("diff", "--name-only", "--diff-filter=U")
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}

// Commit is a one-line summary of a commit.
type Commit struct {
	SHA     string
	Subject string
	Time    time.Time
}

// LastCommit returns the tip commit of rev.
func (r *Repo) LastCommit(rev string) (Commit, error) {
	out, err := r.Run("log", "-1", "--format=%H%x00%ct%x00%s", rev, "--")
	if err != nil {
		return Commit{}, err
	}
	parts := strings.SplitN(out, "\x00", 3)
	if len(parts) != 3 {
		return Commit{}, fmt.Errorf("unexpected git log output %q", out)
	}
	secs, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Commit{}, err
	}
	return Commit{SHA: parts[0], Subject: parts[2], Time: time.Unix(secs, 0)}, nil
}

// ValidBranchName reports whether name is an acceptable branch name.
// `check-ref-format --branch` expands @{-N} and accepts "@", so those are rejected here.
func (r *Repo) ValidBranchName(name string) bool {
	if strings.HasPrefix(name, "-") || name == "@" || strings.Contains(name, "@{") {
		return false
	}
	_, err := r.Run("check-ref-format", "--branch", name)
	return err == nil
}

// RunEnv executes git with extra environment variables.
func (r *Repo) RunEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", &Error{Args: args, Stderr: stderr.String() + stdout.String(), Err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// Includes reports whether sha is, or once was, part of the local branch: an
// ancestor of its tip or of one of its reflog entries. A remote commit that
// is not included was never integrated locally.
func (r *Repo) Includes(branch, sha string) bool {
	ref := "refs/heads/" + branch
	if r.IsAncestor(sha, ref) {
		return true
	}
	out, err := r.Run("log", "-g", "--format=%H", ref, "--")
	if err != nil || out == "" {
		return false
	}
	entries := strings.Split(out, "\n")
	for _, e := range entries {
		if e == sha {
			return true
		}
	}
	for i, e := range entries {
		if i == maxReflogChecks {
			break
		}
		if r.IsAncestor(sha, e) {
			return true
		}
	}
	return false
}

// maxReflogChecks bounds the ancestry checks Includes makes against reflog entries.
const maxReflogChecks = 30

// SamePath reports whether two paths name the same directory, resolving symlinks.
func SamePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	if err1 != nil || err2 != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// ForkPoint returns `git merge-base --fork-point base branch`.
func (r *Repo) ForkPoint(base, branch string) (string, error) {
	return r.Run("merge-base", "--fork-point", base, branch)
}

// ConflictLines returns the 1-based line numbers of conflict markers in file,
// a path relative to the worktree root as ConflictedFiles returns it.
func (r *Repo) ConflictLines(file string) []int {
	top, err := r.TopLevel()
	if err != nil {
		top = r.Dir
	}
	data, err := os.ReadFile(filepath.Join(top, file))
	if err != nil {
		return nil
	}
	var lines []int
	for i, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "<<<<<<< ") {
			lines = append(lines, i+1)
		}
	}
	return lines
}
