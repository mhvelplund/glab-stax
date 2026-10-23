package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

const (
	mergeMethodKey = "glab-stax.merge-method"
	pollInterval   = 5 * time.Second
)

type mergeOptions struct {
	method                string
	merge, squash, rebase bool
	yes                   bool
	timeout               time.Duration
}

func newMergeCmd(app *App) *cobra.Command {
	var o mergeOptions
	cmd := &cobra.Command{
		Use:   "merge [<mr-number>]",
		Short: "Merge the stack's merge requests, bottom up",
		Long: `Merge the stack's merge requests from the bottom up to and including the
chosen one (by default the top of the stack).

GitLab has no atomic stack merge, so MRs are merged one at a time: after each
merge, the next layer is rebased onto the updated trunk, pushed, retargeted and
merged. If a merge fails, glab-stax stops and reports how far it got; the MRs
merged so far stay merged.

Each merge is requested as auto-merge: GitLab merges at once when nothing is
pending, waits for a running pipeline, or adds the MR to a merge train.
glab-stax waits for each merge to finish (see --timeout).

Only basic state is checked up front (open and not a draft). GitLab enforces
approvals, pipelines and other rules when merging, and reports failures.

--squash squashes each MR. --merge and --rebase both keep the commits; whether
GitLab creates a merge commit or fast-forwards is the project's merge method.
Without a method flag, the last one used is reused, else the project's squash
setting decides.

In a terminal you can pick how far up to merge and the method, then confirm.
With --yes (or outside a terminal) the merge starts without prompting.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runMerge(o, args)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.method, "merge-method", "", "merge method: merge, squash or rebase")
	f.BoolVar(&o.merge, "merge", false, "keep commits (merge commit or fast-forward, per project setting)")
	f.BoolVar(&o.squash, "squash", false, "squash each MR")
	f.BoolVar(&o.rebase, "rebase", false, "keep commits on top of the trunk")
	f.BoolVarP(&o.yes, "yes", "y", false, "merge without prompting")
	f.DurationVar(&o.timeout, "timeout", time.Hour, "how long to wait for each MR to merge (pipelines, merge trains)")
	return cmd
}

func (o mergeOptions) methodFlag() (string, error) {
	var set []string
	for name, on := range map[string]bool{"merge": o.merge, "squash": o.squash, "rebase": o.rebase} {
		if on {
			set = append(set, name)
		}
	}
	if o.method != "" {
		switch o.method {
		case "merge", "squash", "rebase":
			set = append(set, o.method)
		default:
			return "", exitcode.New(exitcode.InvalidArgs, "invalid --merge-method %q (want merge, squash or rebase)", o.method)
		}
	}
	switch {
	case len(set) == 0:
		return "", nil
	case len(set) == 2 && set[0] == set[1]:
		return set[0], nil
	case len(set) > 1:
		return "", exitcode.New(exitcode.InvalidArgs, "choose one merge method")
	}
	return set[0], nil
}

func (a *App) runMerge(o mergeOptions, args []string) error {
	method, err := o.methodFlag()
	if err != nil {
		return err
	}
	return a.mutate(func(f *stack.File) error {
		s, targetIID, err := a.mergeTarget(f, args)
		if err != nil {
			return err
		}
		remote := pickRemote("", s.Remote)
		c, err := a.gitlab(remote)
		if err != nil {
			return err
		}
		known, err := engine.SyncMRs(c, s)
		if err != nil {
			return err
		}
		candidates, err := mergeCandidates(s, known, targetIID)
		if err != nil {
			return err
		}
		if method == "" {
			method = a.defaultMergeMethod(c)
		}
		if a.Interactive && !o.yes {
			if candidates, method, err = a.mergeWizard(s, candidates, method); err != nil {
				return err
			}
		}
		_, _ = a.repo().Run("config", mergeMethodKey, method)

		st, err := a.store()
		if err != nil {
			return err
		}
		m := &merger{app: a, s: s, c: c, known: known, remote: remote, squash: method == "squash", timeout: o.timeout,
			r: &engine.Rebaser{Repo: a.repo(), Store: st, Log: a.infof}}
		if err := m.r.Preflight(branchNames(s.Active())); err != nil {
			return err
		}
		if err := m.run(candidates); err != nil {
			if serr := a.saveNow(f); serr != nil {
				a.infof("Warning: could not save stack metadata: %v", serr)
			}
			return err
		}
		return nil
	})
}

// mergeTarget finds the stack and the MR to merge up to (0 = top of the stack).
func (a *App) mergeTarget(f *stack.File, args []string) (*stack.Stack, int, error) {
	if len(args) == 0 {
		s, _, err := a.currentStack(f)
		return s, 0, err
	}
	iid, ok := parseMRRef(args[0])
	if !ok {
		return nil, 0, exitcode.New(exitcode.InvalidArgs, "expected an MR number, got %q", args[0])
	}
	if s, _ := findLocalMR(f, iid); s != nil {
		return s, iid, nil
	}
	// A stack that is only on GitLab: set it up locally without switching branches.
	c, err := a.gitlab(a.defaultRemote())
	if err != nil {
		return nil, 0, err
	}
	mr, err := c.GetMR(iid)
	if err != nil {
		return nil, 0, exitcode.Wrap(exitcode.NotInStack, fmt.Errorf("!%d: %w", iid, err))
	}
	info, err := remoteStackFor(c, mr)
	if err != nil {
		return nil, 0, err
	}
	s, err := a.setupRemoteStack(f, c, info, a.defaultRemote())
	return s, iid, err
}

// mergeCandidates returns the unmerged layers from the bottom up to targetIID
// (or the top), checking that each has an open, non-draft MR.
func mergeCandidates(s *stack.Stack, known map[string]*glab.MR, targetIID int) ([]*stack.Branch, error) {
	var out []*stack.Branch
	for _, b := range s.Active() {
		mr := known[b.Name]
		switch {
		case mr == nil:
			return nil, exitcode.New(exitcode.Generic, "%s has no merge request; run `glab stax submit` first", b.Name)
		case !mr.Open():
			return nil, exitcode.New(exitcode.Generic, "!%d (%s) is %s", mr.IID, b.Name, mr.State)
		case mr.Draft:
			return nil, exitcode.New(exitcode.Generic, "!%d (%s) is a draft; mark it ready first (e.g. `glab stax submit --open`)", mr.IID, b.Name)
		}
		out = append(out, b)
		if mr.IID == targetIID {
			return out, nil
		}
	}
	if targetIID != 0 {
		return nil, exitcode.New(exitcode.Generic, "!%d is already merged", targetIID)
	}
	if len(out) == 0 {
		return nil, exitcode.New(exitcode.Generic, "every MR in the stack is already merged")
	}
	return out, nil
}

func (a *App) defaultMergeMethod(c *glab.Client) string {
	if m, err := a.repo().Run("config", "--get", mergeMethodKey); err == nil && m != "" {
		return m
	}
	if p, err := c.GetProject(); err == nil && (p.SquashOption == "always" || p.SquashOption == "default_on") {
		return "squash"
	}
	return "merge"
}

func (a *App) mergeWizard(s *stack.Stack, candidates []*stack.Branch, method string) ([]*stack.Branch, string, error) {
	if len(candidates) > 1 {
		var opts []string
		for i := len(candidates) - 1; i >= 0; i-- {
			opts = append(opts, fmt.Sprintf("!%d %s", candidates[i].MR.IID, candidates[i].Name))
		}
		choice, err := a.choose("Merge up to and including which MR? (everything below it is merged too)", opts)
		if err != nil {
			return nil, "", err
		}
		candidates = candidates[:len(candidates)-choice]
	}
	methods := []string{"merge", "squash", "rebase"}
	for i, m := range methods {
		if m == method {
			methods[0], methods[i] = methods[i], methods[0]
		}
	}
	choice, err := a.choose("Merge method:", methods)
	if err != nil {
		return nil, "", err
	}
	method = methods[choice]
	var list []string
	for _, b := range candidates {
		list = append(list, fmt.Sprintf("!%d", b.MR.IID))
	}
	ok, err := a.confirm(fmt.Sprintf("Merge %s into %s (%s)?", strings.Join(list, ", "), s.Trunk, method), true)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", exitcode.New(exitcode.Generic, "merge cancelled")
	}
	return candidates, method, nil
}

type merger struct {
	app     *App
	s       *stack.Stack
	c       *glab.Client
	r       *engine.Rebaser
	known   map[string]*glab.MR
	remote  string
	squash  bool
	timeout time.Duration
}

func (m *merger) run(candidates []*stack.Branch) error {
	var merged []string
	pendingDelete := ""
	fail := func(err error) error {
		if len(merged) > 0 {
			m.app.infof("Merged so far: %s. The rest of the stack is unchanged.", strings.Join(merged, ", "))
		}
		return err
	}
	for i, b := range candidates {
		if i > 0 {
			if err := m.restack(b); err != nil {
				return fail(err)
			}
		}
		if pendingDelete != "" {
			m.deleteRemoteBranch(pendingDelete)
			pendingDelete = ""
		}
		mr, err := m.mergeOne(b)
		if err != nil {
			return fail(err)
		}
		b.Merged = true
		merged = append(merged, fmt.Sprintf("!%d", mr.IID))
		m.app.infof("Merged !%d (%s) into %s", mr.IID, b.Name, m.s.Trunk)
		if m.known[b.Name].RemoveSource {
			pendingDelete = b.Name
		}
	}
	// Restack whatever is left above the merged layers.
	if rest := m.s.Bottom(); rest != nil {
		if err := m.restack(rest); err != nil {
			return fail(err)
		}
		if err := engine.UpdateBlocks(m.c, m.s, m.known); err != nil {
			return fail(err)
		}
	} else if _, err := m.r.UpdateFromRemote(m.s, m.remote); err != nil {
		return fail(err)
	}
	if pendingDelete != "" {
		m.deleteRemoteBranch(pendingDelete)
	}
	m.app.infof("Merged %s into %s", strings.Join(merged, ", "), m.s.Trunk)
	return nil
}

// restack rebases b and everything above it onto the trunk, pushes, and
// points b's MR at the trunk.
func (m *merger) restack(b *stack.Branch) error {
	if err := m.r.Start(m.s, engine.RebaseOptions{Anchor: b.Name, Upstack: true, Remote: m.remote, AbortOnConflict: true}); err != nil {
		return err
	}
	if err := engine.Push(m.app.repo(), m.remote, branchNames(m.s.Active())); err != nil {
		return err
	}
	mr := m.known[b.Name]
	if target := m.s.Parent(b.Name); mr != nil && mr.TargetBranch != target {
		updated, err := m.c.UpdateMR(mr.IID, map[string]any{"target_branch": target})
		if err != nil {
			return err
		}
		m.known[b.Name] = updated
		m.app.infof("Retargeted !%d to %s", mr.IID, target)
	}
	return nil
}

// mergeOne merges b's MR at the local branch tip and waits until it is merged.
func (m *merger) mergeOne(b *stack.Branch) (*glab.MR, error) {
	iid := m.known[b.Name].IID
	sha, err := m.app.repo().RevParse("refs/heads/" + b.Name)
	if err != nil {
		return nil, err
	}
	deadline := m.app.Now().Add(m.timeout)
	mr, err := m.c.GetMR(iid)
	for err == nil && mr.Checking() && m.app.Now().Before(deadline) {
		m.app.Sleep(pollInterval)
		mr, err = m.c.GetMR(iid)
	}
	if err != nil {
		return nil, err
	}
	m.app.infof("Merging !%d (%s)...", iid, b.Name)
	if mr, err = m.c.Merge(iid, m.squash, sha); err != nil {
		return nil, fmt.Errorf("!%d could not be merged: %w", iid, err)
	}
	waiting := false
	for !mr.Merged() {
		switch {
		case mr.State != "opened":
			return nil, exitcode.New(exitcode.GitLabFailure, "!%d was %s instead of merged", iid, mr.State)
		case !mr.AutoMerge:
			reason := mr.MergeError
			if reason == "" {
				reason = "auto-merge was cancelled"
			}
			return nil, exitcode.New(exitcode.GitLabFailure, "!%d did not merge: %s", iid, reason)
		case !m.app.Now().Before(deadline):
			return nil, exitcode.New(exitcode.GitLabFailure, "timed out waiting for !%d to merge; it is still set to auto-merge", iid)
		}
		if !waiting {
			m.app.infof("Waiting for !%d to merge (pipeline or merge train)...", iid)
			waiting = true
		}
		m.app.Sleep(pollInterval)
		if mr, err = m.c.GetMR(iid); err != nil {
			return nil, err
		}
	}
	return mr, nil
}

func (m *merger) deleteRemoteBranch(branch string) {
	if _, err := m.app.repo().Run("push", "--quiet", m.remote, "--delete", branch); err != nil {
		m.app.infof("Warning: could not delete %s on %s: %v", branch, m.remote, err)
		return
	}
	m.app.infof("Deleted %s on %s", branch, m.remote)
}
