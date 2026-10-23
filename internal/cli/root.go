// Package cli wires the glab-stax commands.
package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time with -ldflags.
var Version = "dev"

// NewRoot builds the root command.
func NewRoot(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "glab-stax",
		Short:         "Stacked merge requests for GitLab, gh-stack style",
		Long:          "glab-stax manages stacks of branches, each with any number of commits,\nand one GitLab merge request per branch. Invoke it as `glab stax` after\nrunning `glab-stax alias`.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetFlagErrorFunc(flagError)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	root.AddCommand(
		newInitCmd(app),
		newAddCmd(app),
		newViewCmd(app),
		newRebaseCmd(app),
		newPushCmd(app),
		newSubmitCmd(app),
		newSyncCmd(app),
		newMergeCmd(app),
		newCheckoutCmd(app),
		newSwitchCmd(app),
		newModifyCmd(app),
		newLinkCmd(app),
		newUnstackCmd(app),
	)
	root.AddCommand(newNavCmds(app)...)
	wrapArgs(root)
	return root
}
