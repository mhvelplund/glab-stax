package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
	"github.com/mhvelplund/glab-stax/internal/tui"
)

func newModifyCmd(app *App) *cobra.Command {
	var cont, abort bool
	cmd := &cobra.Command{
		Use:   "modify",
		Short: "Restructure the stack in a full-screen editor",
		Long: `Open a full-screen editor to restructure the current stack. Changes are
staged and applied together with ^s:

  x            drop a layer (its commits leave the stack)
  d / u        fold a layer into the one below / above
  i / I        insert an empty layer below / above
  shift+↓/↑    move a layer down / up
  r            rename a layer
  z            undo
  f / c        show a layer's files / commits

Dropped and folded branches keep their local branch and MR. Changed layers are
rebuilt by cherry-picking their commits; branches only move once everything
applied. On a conflict, resolve it, git add, and run --continue, or cancel
with --abort. Run submit afterwards to update the merge requests.

Requires a clean worktree, linear history, and no MR set to auto-merge.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runModify(cont, abort)
		},
	}
	cmd.Flags().BoolVar(&cont, "continue", false, "continue after resolving a conflict")
	cmd.Flags().BoolVar(&abort, "abort", false, "cancel a paused modify")
	cmd.MarkFlagsMutuallyExclusive("continue", "abort")
	return cmd
}

func (a *App) runModify(cont, abort bool) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	m := &engine.Modifier{Repo: a.repo(), Store: st, Log: a.infof}
	return a.mutateWith(true, func(f *stack.File) error {
		switch {
		case cont:
			return m.Continue(f)
		case abort:
			return m.Abort()
		}
		if err := checkPaused(st); err != nil {
			return err
		}
		if !a.Interactive {
			return exitcode.New(exitcode.InvalidArgs, "modify needs a terminal")
		}
		s, _, err := a.currentStack(f)
		if err != nil {
			return err
		}
		if len(s.Active()) == 0 {
			return exitcode.New(exitcode.Generic, "every layer is merged; nothing to modify")
		}
		r := &engine.Rebaser{Repo: a.repo(), Store: st, Log: a.infof}
		if err := r.Preflight(branchNames(s.Active())); err != nil {
			return err
		}
		if err := a.checkNoAutoMerge(s); err != nil {
			return err
		}
		ranges, err := m.Ranges(s)
		if err != nil {
			return err
		}
		if err := m.CheckLinear(ranges); err != nil {
			return err
		}
		var nodes []*tui.ModifyNode
		for _, b := range s.Active() {
			n, _ := a.repo().CommitCount(ranges[b.Name].Base, ranges[b.Name].Tip)
			nodes = append(nodes, &tui.ModifyNode{Name: b.Name, Orig: b.Name, Sources: []string{b.Name}, Commits: n})
		}
		e := tui.NewModifyEditor(nodes)
		e.Details = func(branch, kind string) string {
			rg := ranges[branch].Base + ".." + ranges[branch].Tip
			args := []string{"log", "--oneline", rg}
			if kind == "files" {
				args = []string{"diff", "--stat", rg}
			}
			out, err := a.repo().Run(args...)
			if err != nil || strings.TrimSpace(out) == "" {
				return "(none)"
			}
			return out
		}
		if _, err := a.runTUI(e); err != nil {
			return err
		}
		if !e.Applied {
			a.infof("No changes made")
			return nil
		}
		var layers []engine.Layer
		for _, n := range e.Nodes {
			if !n.Dropped {
				layers = append(layers, engine.Layer{Name: n.Name, Orig: n.Orig, Sources: n.Sources})
			}
		}
		return m.Apply(s, layers)
	})
}

// checkNoAutoMerge refuses to restructure while an MR is set to auto-merge,
// since it could merge mid-change. GitLab being unreachable is not fatal.
func (a *App) checkNoAutoMerge(s *stack.Stack) error {
	c, err := a.gitlab(pickRemote("", s.Remote))
	if err != nil {
		return nil
	}
	known, err := engine.SyncMRs(c, s)
	if err != nil {
		return nil
	}
	for _, b := range s.Active() {
		if mr := known[b.Name]; mr != nil && mr.Open() && mr.AutoMerge {
			return exitcode.New(exitcode.Generic, "!%d is set to auto-merge; cancel that before modifying the stack", mr.IID)
		}
	}
	return nil
}
