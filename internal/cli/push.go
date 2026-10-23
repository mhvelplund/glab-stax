package cli

import (
	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func newPushCmd(app *App) *cobra.Command {
	var remote string
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Push every unmerged branch of the stack",
		Long: `Push every unmerged branch in one git push, each with --force-with-lease.
Merge requests are not created or updated; use submit for that. The push is
not atomic: some branches may be updated even if others are rejected.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runPush(remote)
		},
	}
	cmd.Flags().StringVar(&remote, "remote", "", "remote to push to (default: the stack's remote)")
	return cmd
}

func (a *App) runPush(remoteFlag string) error {
	return a.mutate(func(f *stack.File) error {
		s, _, err := a.currentStack(f)
		if err != nil {
			return err
		}
		names := branchNames(s.Active())
		if len(names) == 0 {
			a.infof("All branches are merged; nothing to push")
			return nil
		}
		remote := pickRemote(remoteFlag, s.Remote)
		if err := engine.Push(a.repo(), remote, names); err != nil {
			return err
		}
		a.infof("Pushed %d branch(es) to %s", len(names), remote)
		return nil
	})
}

func branchNames(bs []*stack.Branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Name
	}
	return out
}
