package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

func flagError(_ *cobra.Command, err error) error {
	return exitcode.Wrap(exitcode.InvalidArgs, err)
}

// wrapArgs makes positional-argument validation errors exit with InvalidArgs.
func wrapArgs(cmd *cobra.Command) {
	if v := cmd.Args; v != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			return exitcode.Wrap(exitcode.InvalidArgs, v(c, args))
		}
	}
	for _, c := range cmd.Commands() {
		wrapArgs(c)
	}
}

// Execute runs the root command and maps cobra's own usage errors to InvalidArgs.
func Execute(app *App, args []string) error {
	notify := app.updateNotifier()
	defer notify()
	root := NewRoot(app)
	root.SetArgs(args)
	err := root.Execute()
	if exitcode.Of(err) == exitcode.Generic {
		msg := err.Error()
		if strings.HasPrefix(msg, "unknown command") || strings.HasPrefix(msg, "if any flags in the group") {
			return exitcode.Wrap(exitcode.InvalidArgs, err)
		}
	}
	return err
}
