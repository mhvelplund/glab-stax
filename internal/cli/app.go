package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
	"github.com/mhvelplund/glab-stax/internal/tui"
)

// App is the environment commands run in. Tests construct it directly.
type App struct {
	Dir         string
	In          io.Reader
	Out, Err    io.Writer
	Interactive bool // stdin and stdout are terminals
	OutIsTTY    bool
	Now         func() time.Time
	// Sleep waits between GitLab polls (tests make it instant).
	Sleep func(time.Duration)
	// RunTUI runs a full-screen editor (tests drive it with scripted keys).
	RunTUI func(m tea.Model) (tea.Model, error)
	// NewGitLab overrides how GitLab clients are built (tests use a fake).
	NewGitLab func(remote string) (*glab.Client, error)

	in *bufio.Reader
}

// NewApp returns an App bound to the process's stdio and working directory.
func NewApp() (*App, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	outTTY := term.IsTerminal(int(os.Stdout.Fd()))
	return &App{
		Dir:         dir,
		In:          os.Stdin,
		Out:         os.Stdout,
		Err:         os.Stderr,
		Interactive: outTTY && term.IsTerminal(int(os.Stdin.Fd())),
		OutIsTTY:    outTTY,
		Now:         time.Now,
		Sleep:       time.Sleep,
	}, nil
}

// runTUI runs m on the terminal, drawing on stderr so stdout stays clean.
func (a *App) runTUI(m tea.Model) (tea.Model, error) {
	if a.RunTUI != nil {
		return a.RunTUI(m)
	}
	return tui.Run(m, a.In, a.Err)
}

func (a *App) repo() *git.Repo { return &git.Repo{Dir: a.Dir} }

func (a *App) store() (stack.Store, error) {
	dir, err := a.repo().CommonDir()
	if err != nil {
		return stack.Store{}, fmt.Errorf("not a git repository: %w", err)
	}
	return stack.Store{Dir: dir}, nil
}

// load reads the metadata without locking, for read-only commands.
func (a *App) load() (*stack.File, error) {
	st, err := a.store()
	if err != nil {
		return nil, err
	}
	return st.Load()
}

// mutate locks the store, loads it, runs fn and saves the result if fn succeeds.
// It refuses to run while a multi-step operation is paused.
func (a *App) mutate(fn func(f *stack.File) error) error {
	return a.mutateWith(false, fn)
}

func (a *App) mutateWith(allowPaused bool, fn func(f *stack.File) error) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	unlock, err := st.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	if !allowPaused {
		if err := checkPaused(st); err != nil {
			return err
		}
	}
	f, err := st.Load()
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	return st.Save(f)
}

func checkPaused(st stack.Store) error {
	switch {
	case st.HasJournal(stack.RebaseJournal):
		return exitcode.New(exitcode.RebaseInProgress, "a stack rebase is paused; run `glab stax rebase --continue` or `--abort` first")
	case st.HasJournal(stack.ModifyJournal):
		return exitcode.New(exitcode.RebaseInProgress, "a stack modify is paused; run `glab stax modify --continue` or `--abort` first")
	}
	return nil
}

// currentStack resolves the stack for the checked-out branch.
func (a *App) currentStack(f *stack.File) (*stack.Stack, string, error) {
	cur, err := a.repo().CurrentBranch()
	if err != nil {
		return nil, "", err
	}
	if cur == "" {
		return nil, "", fmt.Errorf("HEAD is detached; check out a stack branch")
	}
	s, err := f.Resolve(cur)
	return s, cur, err
}

func (a *App) infof(format string, args ...any) {
	fmt.Fprintf(a.Err, format+"\n", args...)
}

func (a *App) reader() *bufio.Reader {
	if a.in == nil {
		a.in = bufio.NewReader(a.In)
	}
	return a.in
}

// input prompts for a line of text.
func (a *App) input(prompt string) (string, error) {
	fmt.Fprintf(a.Err, "%s ", prompt)
	line, err := a.reader().ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// confirm asks a yes/no question.
func (a *App) confirm(prompt string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	ans, err := a.input(prompt + " " + hint)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(ans) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// choose asks the user to pick one of options and returns its index.
func (a *App) choose(prompt string, options []string) (int, error) {
	fmt.Fprintln(a.Err, prompt)
	for i, o := range options {
		fmt.Fprintf(a.Err, "  %d) %s\n", i+1, o)
	}
	for {
		ans, err := a.input(fmt.Sprintf("Choose 1-%d:", len(options)))
		if err != nil {
			return 0, err
		}
		var n int
		if _, err := fmt.Sscanf(ans, "%d", &n); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
	}
}
