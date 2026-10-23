package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/tui"
)

func newSwitchCmd(app *App) *cobra.Command {
	var printPath bool
	cmd := &cobra.Command{
		Use:   "switch",
		Short: "Interactively switch to another branch in the stack",
		Long: `Show a picker listing the branches of the current stack, top first, and
switch to the selected one. Use ↑/↓ or j/k and Enter; / searches.

To move one branch without a picker, use up or down.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runSwitch(printPath)
		},
	}
	cmd.Flags().BoolVar(&printPath, "print-path", false, "print the path of the worktree that has the target branch checked out")
	return cmd
}

func (a *App) runSwitch(printPath bool) error {
	if !a.Interactive {
		return exitcode.New(exitcode.InvalidArgs, "switch needs a terminal; use up, down, top, bottom or checkout")
	}
	f, err := a.load()
	if err != nil {
		return err
	}
	s, cur, err := a.currentStack(f)
	if err != nil {
		return err
	}
	active := s.Active()
	var items []tui.Item
	var targets []string
	initial := len(active) // the trunk, unless the current branch is listed
	for i := len(active) - 1; i >= 0; i-- {
		b := active[i]
		detail := ""
		if b.MR != nil {
			detail = fmt.Sprintf("!%d", b.MR.IID)
		}
		if b.Name == cur {
			initial = len(items)
		}
		items = append(items, tui.Item{Title: fmt.Sprintf("%d. %s", i+1, b.Name), Detail: detail})
		targets = append(targets, b.Name)
	}
	items = append(items, tui.Item{Title: s.Trunk, Detail: "trunk"})
	targets = append(targets, s.Trunk)

	p := tui.NewPicker("Switch branch", items)
	p.Initial = initial
	if _, err := a.runTUI(p); err != nil {
		return err
	}
	if p.Chosen < 0 {
		return nil
	}
	return a.goTo(targets[p.Chosen], printPath)
}
