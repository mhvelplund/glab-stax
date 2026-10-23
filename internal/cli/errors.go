package cli

import (
	"github.com/spf13/cobra"

	"glab-stax/internal/exitcode"
)

func flagError(_ *cobra.Command, err error) error {
	return exitcode.Wrap(exitcode.InvalidArgs, err)
}
