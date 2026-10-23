package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

const wrapperMarker = "# installed by glab-stax alias"

func newAliasCmd(app *App) *cobra.Command {
	var remove bool
	cmd := &cobra.Command{
		Use:   "alias [name]",
		Short: "Register `glab stax` and install a short command (default gs)",
		Long: `Register the glab alias "stax", so glab-stax runs as ` + "`glab stax`" + `, and
install a wrapper script in ~/.local/bin (default name gs) so it also runs as
` + "`gs`" + `. Make sure ~/.local/bin is on your PATH.

--remove deletes the wrapper and the glab alias. Not supported on Windows.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := "gs"
			if len(args) == 1 {
				name = args[0]
			}
			return app.runAlias(name, remove)
		},
	}
	cmd.Flags().BoolVar(&remove, "remove", false, "remove the wrapper and the glab alias")
	return cmd
}

func (a *App) runAlias(name string, remove bool) error {
	if runtime.GOOS == "windows" {
		return exitcode.New(exitcode.Generic, "alias is not supported on Windows; run `glab alias set stax \"!glab-stax $@\"` yourself")
	}
	if name == "" || strings.ContainsAny(name, "/ \t") {
		return exitcode.New(exitcode.InvalidArgs, "invalid alias name %q", name)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".local", "bin", name)
	if remove {
		return a.removeAlias(path)
	}
	if data, err := os.ReadFile(path); err == nil && !strings.Contains(string(data), wrapperMarker) {
		return exitcode.New(exitcode.Generic, "%s already exists and was not installed by glab-stax", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	script := "#!/bin/sh\n" + wrapperMarker + "\nexec glab-stax \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	a.infof("Installed %s", path)
	if _, err := a.glabCLI("alias", "set", "stax", `glab-stax "$@"`, "--shell"); err != nil {
		a.infof("Warning: could not register the glab alias: %v", err)
	} else {
		a.infof("Registered `glab stax`")
	}
	if !onPath(filepath.Dir(path)) {
		a.infof("Add %s to your PATH to use `%s`", filepath.Dir(path), name)
	}
	return nil
}

func (a *App) removeAlias(path string) error {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		a.infof("%s does not exist", path)
	case err != nil:
		return err
	case !strings.Contains(string(data), wrapperMarker):
		return exitcode.New(exitcode.Generic, "%s was not installed by glab-stax; not removing it", path)
	default:
		if err := os.Remove(path); err != nil {
			return err
		}
		a.infof("Removed %s", path)
	}
	if _, err := a.glabCLI("alias", "delete", "stax"); err == nil {
		a.infof("Removed `glab stax`")
	}
	return nil
}

func (a *App) glabCLI(args ...string) ([]byte, error) {
	if a.Glab != nil {
		return a.Glab(args...)
	}
	return glabExec(args...)
}

func onPath(dir string) bool {
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(p) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}

func newFeedbackCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "feedback [title]",
		Short: "Open an issue for glab-stax in your browser",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			u := issueURL(strings.Join(args, " "))
			fmt.Fprintln(app.Out, u)
			if err := app.openURL(u); err != nil {
				app.infof("Could not open a browser: %v", err)
			}
			return nil
		},
	}
}
