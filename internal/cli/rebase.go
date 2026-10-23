package cli

import (
	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

type rebaseFlags struct {
	opts        engine.RebaseOptions
	cont, abort bool
}

func newRebaseCmd(app *App) *cobra.Command {
	var fl rebaseFlags
	cmd := &cobra.Command{
		Use:   "rebase [branch]",
		Short: "Rebase every layer onto the one below it",
		Long: `Fetch from the remote, then rebase each branch in order from the trunk up,
so every layer contains the tip of the layer below it.

Layers whose merge request was merged are skipped, and the layers above them
are rebased --onto the nearest unmerged layer. On conflict the rebase pauses:
resolve, git add, then run --continue. --abort restores every branch.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				fl.opts.Anchor = args[0]
			}
			return app.runRebase(fl)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&fl.opts.Downstack, "downstack", false, "only rebase from the trunk up to the current branch")
	f.BoolVar(&fl.opts.Upstack, "upstack", false, "only rebase from the current branch to the top")
	f.BoolVar(&fl.opts.NoTrunk, "no-trunk", false, "skip fetching and rebasing onto the trunk")
	f.StringVar(&fl.opts.Remote, "remote", "", "remote to fetch from (default: the stack's remote)")
	f.BoolVar(&fl.opts.PreserveDates, "committer-date-is-author-date", false, "keep committer dates equal to author dates")
	f.BoolVar(&fl.opts.PreserveDates, "preserve-dates", false, "alias for --committer-date-is-author-date")
	f.BoolVar(&fl.cont, "continue", false, "continue after resolving a conflict")
	f.BoolVar(&fl.abort, "abort", false, "abort and restore all branches")
	cmd.MarkFlagsMutuallyExclusive("continue", "abort")
	cmd.MarkFlagsMutuallyExclusive("downstack", "upstack")
	return cmd
}

func (a *App) runRebase(fl rebaseFlags) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	r := &engine.Rebaser{Repo: a.repo(), Store: st, Log: a.infof}
	return a.mutateWith(true, func(f *stack.File) error {
		switch {
		case fl.cont:
			return r.Continue(f)
		case fl.abort:
			return r.Abort(f)
		}
		if err := checkPaused(st); err != nil {
			return err
		}
		var s *stack.Stack
		if fl.opts.Anchor != "" {
			if s, err = f.Resolve(fl.opts.Anchor); err != nil {
				return err
			}
		} else {
			var cur string
			if s, cur, err = a.currentStack(f); err != nil {
				return err
			}
			fl.opts.Anchor = cur
		}
		if len(s.Branches) == 0 {
			return exitcode.New(exitcode.NotInStack, "the stack has no branches")
		}
		// Best effort: learn which layers were merged so they are skipped.
		if c, err := a.gitlab(pickRemote(fl.opts.Remote, s.Remote)); err == nil {
			_, _ = engine.SyncMRs(c, s)
		}
		if err := a.repo().EnableRerere(); err != nil {
			return err
		}
		return r.Start(s, fl.opts)
	})
}
