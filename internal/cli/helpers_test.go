package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/glab/glabtest"
	"github.com/mhvelplund/glab-stax/internal/stack"
	"github.com/mhvelplund/glab-stax/internal/testutil"
)

type harness struct {
	t    *testing.T
	repo *testutil.Repo
	gl   *glabtest.GitLab
	// runner, when set, replaces the fake's glab runner (e.g. to inject failures).
	runner glab.Runner
	// clock advances by tick on every Now() call when tick > 0.
	clock time.Time
	tick  time.Duration
	slept time.Duration
}

func (h *harness) now() time.Time {
	if h.clock.IsZero() {
		h.clock = time.Date(2026, 3, 24, 12, 0, 0, 0, time.UTC)
	}
	h.clock = h.clock.Add(h.tick)
	return h.clock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("GLAB_STAX_HYPERLINKS", "0")
	return &harness{t: t, repo: testutil.NewRepo(t), gl: glabtest.New()}
}

type result struct {
	out, err string
	e        error
}

// run executes a command non-interactively in dir (default: the main clone).
func (h *harness) run(args ...string) result { return h.runIn(h.repo.Dir, "", false, args...) }

func (h *harness) runIn(dir, stdin string, interactive bool, args ...string) result {
	h.t.Helper()
	var out, errb bytes.Buffer
	app := &App{
		Dir: dir, In: strings.NewReader(stdin), Out: &out, Err: &errb,
		Interactive: interactive,
		Now:         h.now,
		Sleep:       func(d time.Duration) { h.slept += d },
		NewGitLab: func(string) (*glab.Client, error) {
			c := h.gl.Client()
			if h.runner != nil {
				c.Run = h.runner
			}
			return c, nil
		},
	}
	e := Execute(app, args)
	return result{out: out.String(), err: errb.String(), e: e}
}

func (h *harness) must(args ...string) result {
	h.t.Helper()
	r := h.run(args...)
	if r.e != nil {
		h.t.Fatalf("%v: %v\nstderr: %s", args, r.e, r.err)
	}
	return r
}

func (h *harness) current() string {
	return h.repo.Git("rev-parse", "--abbrev-ref", "HEAD")
}

func (h *harness) file() *stack.File {
	h.t.Helper()
	f, err := stack.Store{Dir: h.repo.Dir + "/.git"}.Load()
	if err != nil {
		h.t.Fatal(err)
	}
	return f
}

func names(s *stack.Stack) []string {
	var out []string
	for _, b := range s.Branches {
		out = append(out, b.Name)
	}
	return out
}
