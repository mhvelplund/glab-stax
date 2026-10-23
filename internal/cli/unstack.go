package cli

import (
	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func newUnstackCmd(app *App) *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:     "unstack [<mr-number>]",
		Aliases: []string{"delete"},
		Short:   "Remove a stack locally and on GitLab",
		Long: `Remove a stack from local tracking and remove the stack block from its
merge requests on GitLab. Branches and MRs themselves are kept.

With no argument, the current stack is unstacked. With an MR number, the stack
containing that MR is unstacked on GitLab, and locally too if it is tracked.
--local only removes local tracking.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runUnstack(args, local)
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "only remove local tracking")
	return cmd
}

func (a *App) runUnstack(args []string, localOnly bool) error {
	return a.mutate(func(f *stack.File) error {
		var s *stack.Stack
		var iids []int
		if len(args) == 1 {
			iid, ok := parseMRRef(args[0])
			if !ok {
				return exitcode.New(exitcode.InvalidArgs, "expected an MR number, got %q", args[0])
			}
			s, _ = findLocalMR(f, iid)
			if s == nil && localOnly {
				return exitcode.New(exitcode.NotInStack, "!%d is not part of a local stack", iid)
			}
			if !localOnly {
				c, err := a.gitlab(a.defaultRemote())
				if err != nil {
					return err
				}
				mr, err := c.GetMR(iid)
				if err != nil {
					return err
				}
				info, ok := glab.ParseBlock(mr.Description)
				if !ok && s == nil {
					return exitcode.New(exitcode.NotInStack, "!%d is not part of a stack", iid)
				}
				if ok {
					for _, m := range info.MRs {
						iids = append(iids, m.IID)
					}
				}
			}
		} else {
			var err error
			if s, _, err = a.currentStack(f); err != nil {
				return err
			}
		}
		if s != nil {
			for _, b := range s.Branches {
				if b.MR != nil {
					iids = append(iids, b.MR.IID)
				}
			}
		}
		if !localOnly && len(iids) > 0 {
			remote := a.defaultRemote()
			if s != nil {
				remote = pickRemote("", s.Remote)
			}
			c, err := a.gitlab(remote)
			if err != nil {
				return err
			}
			if err := a.stripBlocks(c, iids); err != nil {
				return err
			}
		}
		if s != nil {
			f.Remove(s)
			a.infof("Removed local tracking for the stack on %s", s.Trunk)
		}
		return nil
	})
}

// stripBlocks removes the stack block from each open MR.
func (a *App) stripBlocks(c *glab.Client, iids []int) error {
	seen := map[int]bool{}
	for _, iid := range iids {
		if seen[iid] {
			continue
		}
		seen[iid] = true
		mr, err := c.GetMR(iid)
		if err != nil {
			return err
		}
		if !mr.Open() {
			continue
		}
		if desc := glab.StripBlock(mr.Description); desc != mr.Description {
			if _, err := c.UpdateMR(iid, map[string]any{"description": desc}); err != nil {
				return err
			}
			a.infof("Removed the stack block from !%d", iid)
		}
	}
	return nil
}
