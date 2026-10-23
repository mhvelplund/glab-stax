package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func newNavCmds(app *App) []*cobra.Command {
	type spec struct {
		use, short string
		takesN     bool
		target     func(s *stack.Stack, cur string, n int) (string, string)
	}
	specs := []spec{
		{"up [n]", "Check out a branch further up the stack (away from the trunk)", true, stepUp},
		{"down [n]", "Check out a branch further down the stack (toward the trunk)", true, stepDown},
		{"top", "Check out the top branch of the stack", false, func(s *stack.Stack, _ string, _ int) (string, string) {
			return branchName(s.Top()), "Already at the top of the stack"
		}},
		{"bottom", "Check out the bottom branch of the stack", false, func(s *stack.Stack, _ string, _ int) (string, string) {
			return branchName(s.Bottom()), "Already at the bottom of the stack"
		}},
		{"trunk", "Check out the stack's trunk branch", false, func(s *stack.Stack, _ string, _ int) (string, string) {
			return s.Trunk, "Already on the trunk"
		}},
	}
	var cmds []*cobra.Command
	for _, sp := range specs {
		var printPath bool
		args := cobra.NoArgs
		if sp.takesN {
			args = cobra.MaximumNArgs(1)
		}
		cmd := &cobra.Command{
			Use:   sp.use,
			Short: sp.short + ". Merged branches are skipped.",
			Args:  args,
			RunE: func(_ *cobra.Command, args []string) error {
				n := 1
				if len(args) == 1 {
					v, err := strconv.Atoi(args[0])
					if err != nil || v < 1 {
						return exitcode.New(exitcode.InvalidArgs, "n must be a positive number, got %q", args[0])
					}
					n = v
				}
				return app.navigate(func(s *stack.Stack, cur string) (string, string) { return sp.target(s, cur, n) }, printPath)
			},
		}
		cmd.Flags().BoolVar(&printPath, "print-path", false, "print the path of the worktree that has the target branch checked out")
		cmds = append(cmds, cmd)
	}
	return cmds
}

func branchName(b *stack.Branch) string {
	if b == nil {
		return ""
	}
	return b.Name
}

// stepUp moves n unmerged branches up, stopping at the top. From the trunk it enters the stack.
func stepUp(s *stack.Stack, cur string, n int) (string, string) {
	target := ""
	for i := s.Index(cur) + 1; i < len(s.Branches) && n > 0; i++ {
		if !s.Branches[i].Merged {
			target, n = s.Branches[i].Name, n-1
		}
	}
	return target, "Already at the top of the stack"
}

// stepDown moves n unmerged branches down, stopping at the bottom branch.
func stepDown(s *stack.Stack, cur string, n int) (string, string) {
	target := ""
	for i := s.Index(cur) - 1; i >= 0 && n > 0; i-- {
		if !s.Branches[i].Merged {
			target, n = s.Branches[i].Name, n-1
		}
	}
	return target, "Already at the bottom of the stack"
}

func (a *App) navigate(pick func(s *stack.Stack, cur string) (target, atEnd string), printPath bool) error {
	f, err := a.load()
	if err != nil {
		return err
	}
	s, cur, err := a.currentStack(f)
	if err != nil {
		return err
	}
	target, atEnd := pick(s, cur)
	if target == "" || target == cur {
		a.infof("%s", atEnd)
		target = cur
	}
	return a.goTo(target, printPath)
}

// goTo checks out target here unless another worktree owns it. With
// printPath, the owning worktree's path goes to stdout.
func (a *App) goTo(target string, printPath bool) error {
	g := a.repo()
	owner, err := a.foreignOwner(target)
	if err != nil {
		return err
	}
	if owner != "" {
		if printPath {
			fmt.Fprintln(a.Out, owner)
			return nil
		}
		return exitcode.New(exitcode.Generic, "%s is checked out in worktree %s (use --print-path to cd there)", target, owner)
	}
	cur, err := g.CurrentBranch()
	if err != nil {
		return err
	}
	if cur != target {
		if err := g.Checkout(target); err != nil {
			return err
		}
		a.infof("Switched to %s", target)
	}
	if printPath {
		top, err := g.TopLevel()
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Out, top)
	}
	return nil
}
