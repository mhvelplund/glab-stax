package main

import (
	"fmt"
	"os"
	"strings"

	"glab-stax/internal/cli"
	"glab-stax/internal/exitcode"
)

func main() {
	err := cli.NewRoot().Execute()
	if err != nil {
		code := exitcode.Of(err)
		// Cobra reports unknown commands and bad args as plain errors.
		if code == exitcode.Generic && strings.HasPrefix(err.Error(), "unknown command") {
			code = exitcode.InvalidArgs
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(code)
	}
}
