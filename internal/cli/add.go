package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

type addOptions struct {
	all, update bool
	message     string
}

func newAddCmd(app *App) *cobra.Command {
	var o addOptions
	cmd := &cobra.Command{
		Use:   "add [branch]",
		Short: "Add a branch on top of the stack",
		Long: `Add a branch on top of the stack and check it out. Run it from the top branch.

With -m, staged changes (or all changes with -A, tracked changes with -u) are
committed on the new branch, and the branch name defaults to MM-DD-<slug>.
If the current branch has no commits of its own, the commit lands there instead.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return app.runAdd(o, name)
		},
	}
	cmd.Flags().BoolVarP(&o.all, "all", "A", false, "stage all changes, including untracked files (requires -m)")
	cmd.Flags().BoolVarP(&o.update, "update", "u", false, "stage changes to tracked files (requires -m)")
	cmd.Flags().StringVarP(&o.message, "message", "m", "", "commit staged changes on the new branch with this message")
	cmd.MarkFlagsMutuallyExclusive("all", "update")
	return cmd
}

func (a *App) runAdd(o addOptions, name string) error {
	if (o.all || o.update) && o.message == "" {
		return exitcode.New(exitcode.InvalidArgs, "-A and -u require -m")
	}
	g := a.repo()
	cur, err := g.CurrentBranch()
	if err != nil {
		return err
	}
	if cur == "" {
		return exitcode.New(exitcode.InvalidArgs, "HEAD is detached; check out the top branch of a stack")
	}
	wantsCommit := o.message != ""

	return a.mutate(func(f *stack.File) error {
		s := f.Find(cur)
		if s == nil {
			var err error
			if s, err = a.offerInit(f, cur); err != nil {
				return err
			}
		}
		if top := s.Top(); top != nil && top.Name != cur {
			return exitcode.New(exitcode.InvalidArgs, "run add from the top branch of the stack (%s)", top.Name)
		}

		// A layer without commits of its own takes the commit instead of a new branch.
		if wantsCommit && cur != s.Trunk {
			own, err := g.RevParse(cur)
			if err != nil {
				return err
			}
			parent, err := g.RevParse(s.Parent(cur))
			if err != nil {
				return err
			}
			if own == parent {
				if err := a.stageAndCommit(o); err != nil {
					return err
				}
				a.infof("%s had no commits of its own; committed there instead of creating a new branch", cur)
				return nil
			}
		}

		if name == "" {
			switch {
			case wantsCommit:
				name = dateSlug(a.Now(), o.message)
			case a.Interactive:
				for name == "" {
					if name, err = a.input("Name for the new branch:"); err != nil {
						return err
					}
				}
			default:
				return exitcode.New(exitcode.InvalidArgs, "branch name required")
			}
		}
		if err := a.validateNewBranches(f, s.Trunk, []string{name}); err != nil {
			return err
		}
		owner, err := a.foreignOwner(name)
		if err != nil {
			return err
		}
		if owner != "" && wantsCommit {
			return exitcode.New(exitcode.InvalidArgs, "%s is checked out in %s; cannot commit to it from here", name, owner)
		}
		if wantsCommit {
			if err := a.stage(o); err != nil {
				return err
			}
		}
		b, err := a.addLayer(cur, name)
		if err != nil {
			return err
		}
		if owner == "" {
			if err := g.Checkout(name); err != nil {
				return err
			}
		}
		s.Branches = append(s.Branches, b)
		if wantsCommit {
			if _, err := g.Run("commit", "--quiet", "-m", o.message); err != nil {
				return err
			}
		}
		a.infof("Added %s as layer %d", name, len(s.Branches))
		return nil
	})
}

// offerInit creates a stack for cur when the user agrees. A non-trunk cur
// becomes the first layer.
func (a *App) offerInit(f *stack.File, cur string) (*stack.Stack, error) {
	if !a.Interactive {
		return nil, exitcode.New(exitcode.NotInStack, "branch %q is not part of a stack; run init first", cur)
	}
	ok, err := a.confirm(fmt.Sprintf("%s is not part of a stack. Would you like to initialize a new stack?", cur), true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, exitcode.New(exitcode.NotInStack, "branch %q is not part of a stack", cur)
	}
	g := a.repo()
	remote, err := g.DefaultRemote()
	if err != nil {
		remote = "origin"
	}
	trunk, err := g.DefaultBranch(remote)
	if err != nil {
		return nil, err
	}
	s := &stack.Stack{Trunk: trunk, Remote: remote}
	if cur != trunk {
		b, err := a.addLayer(trunk, cur)
		if err != nil {
			return nil, err
		}
		s.Branches = append(s.Branches, b)
	}
	f.Stacks = append(f.Stacks, s)
	if err := g.EnableRerere(); err != nil {
		return nil, err
	}
	return s, nil
}

// stage applies -A/-u and fails if nothing is staged.
func (a *App) stage(o addOptions) error {
	g := a.repo()
	switch {
	case o.all:
		if _, err := g.Run("add", "-A"); err != nil {
			return err
		}
	case o.update:
		if _, err := g.Run("add", "-u"); err != nil {
			return err
		}
	}
	if _, err := g.Run("diff", "--cached", "--quiet"); err == nil {
		return exitcode.New(exitcode.InvalidArgs, "nothing staged to commit (use -A or -u to stage changes)")
	}
	return nil
}

func (a *App) stageAndCommit(o addOptions) error {
	if err := a.stage(o); err != nil {
		return err
	}
	_, err := a.repo().Run("commit", "--quiet", "-m", o.message)
	return err
}
