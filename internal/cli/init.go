package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func newInitCmd(app *App) *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "init [branches...]",
		Short: "Start a new stack",
		Long: `Start a new stack on top of a trunk branch.

Branches are given bottom to top. Existing branches are adopted, missing
ones are created. With no arguments in a terminal, you are prompted for
names. The trunk defaults to the remote's default branch. Enables git rerere.`,
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runInit(base, args)
		},
	}
	cmd.Flags().StringVarP(&base, "base", "b", "", "trunk branch (default: the remote's default branch)")
	return cmd
}

func (a *App) runInit(base string, names []string) error {
	g := a.repo()
	remote, err := g.DefaultRemote()
	if err != nil {
		remote = "origin"
	}
	trunk := base
	if trunk == "" {
		if trunk, err = g.DefaultBranch(remote); err != nil {
			return exitcode.Wrap(exitcode.InvalidArgs, err)
		}
	}
	if err := a.ensureLocalBranch(remote, trunk); err != nil {
		return err
	}
	cur, err := g.CurrentBranch()
	if err != nil {
		return err
	}

	return a.mutate(func(f *stack.File) error {
		if len(names) == 0 {
			if !a.Interactive {
				return exitcode.New(exitcode.InvalidArgs, "no branches given")
			}
			if names, err = a.promptInitNames(f, cur, trunk); err != nil {
				return err
			}
			if len(names) == 0 {
				return exitcode.New(exitcode.InvalidArgs, "a stack needs at least one branch")
			}
		}
		if err := a.validateNewBranches(f, trunk, names); err != nil {
			return err
		}
		s := &stack.Stack{Trunk: trunk, Remote: remote}
		parent := trunk
		for _, name := range names {
			b, err := a.addLayer(parent, name)
			if err != nil {
				return err
			}
			s.Branches = append(s.Branches, b)
			parent = name
		}
		f.Stacks = append(f.Stacks, s)
		if err := g.EnableRerere(); err != nil {
			return err
		}
		top := names[len(names)-1]
		owner, err := a.foreignOwner(top)
		if err != nil {
			return err
		}
		if owner == "" && top != cur {
			if err := g.Checkout(top); err != nil {
				return err
			}
		}
		a.infof("Created stack on %s with %d branch(es): %v", trunk, len(names), names)
		if owner != "" {
			a.infof("%s is checked out in %s", top, owner)
		}
		return nil
	})
}

// addLayer adopts name if it exists, or creates it at parent's tip.
func (a *App) addLayer(parent, name string) (*stack.Branch, error) {
	g := a.repo()
	if g.BranchExists(name) {
		base, err := g.MergeBase(parent, name)
		if err != nil {
			return nil, err
		}
		return &stack.Branch{Name: name, Base: base}, nil
	}
	base, err := g.RevParse(parent)
	if err != nil {
		return nil, err
	}
	if err := g.CreateBranch(name, base); err != nil {
		return nil, err
	}
	return &stack.Branch{Name: name, Base: base}, nil
}

func (a *App) promptInitNames(f *stack.File, cur, trunk string) ([]string, error) {
	var names []string
	if cur != "" && cur != trunk && f.Find(cur) == nil {
		use, err := a.confirm(fmt.Sprintf("Use the current branch %q as the first layer?", cur), true)
		if err != nil {
			return nil, err
		}
		if use {
			names = append(names, cur)
		}
	}
	for {
		name, err := a.input(fmt.Sprintf("Name for layer %d (empty to finish):", len(names)+1))
		if err != nil || name == "" {
			return names, nil
		}
		names = append(names, name)
	}
}

func (a *App) validateNewBranches(f *stack.File, trunk string, names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		switch {
		case !a.repo().ValidBranchName(n):
			return exitcode.New(exitcode.InvalidArgs, "%q is not a valid branch name", n)
		case n == trunk:
			return exitcode.New(exitcode.InvalidArgs, "%q is the trunk and cannot be a layer", n)
		case seen[n]:
			return exitcode.New(exitcode.InvalidArgs, "%q is listed twice", n)
		case f.Find(n) != nil:
			return exitcode.New(exitcode.InvalidArgs, "%q is already part of a stack", n)
		}
		seen[n] = true
	}
	return nil
}

// ensureLocalBranch creates a local branch from <remote>/<name> if only the remote one exists.
func (a *App) ensureLocalBranch(remote, name string) error {
	g := a.repo()
	if g.BranchExists(name) {
		return nil
	}
	if _, err := g.RevParse("refs/remotes/" + remote + "/" + name); err != nil {
		return exitcode.New(exitcode.InvalidArgs, "branch %q does not exist", name)
	}
	_, err := g.Run("branch", "--track", name, remote+"/"+name)
	return err
}

// foreignOwner returns the path of another worktree that has branch checked out, or "".
func (a *App) foreignOwner(branch string) (string, error) {
	g := a.repo()
	owner, err := g.WorktreeFor(branch)
	if err != nil || owner == "" {
		return "", err
	}
	top, err := g.TopLevel()
	if err != nil {
		return "", err
	}
	if samePath(owner, top) {
		return "", nil
	}
	return owner, nil
}
