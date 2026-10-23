package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func newCheckoutCmd(app *App) *cobra.Command {
	var printPath bool
	cmd := &cobra.Command{
		Use:   "checkout [<mr-number> | <mr-url> | <branch>]",
		Short: "Check out a stack by MR number, MR URL, or branch name",
		Long: `Check out a stack by merge request number (123 or !123), MR URL, or branch name.

A number is looked up in the local stacks first, then on GitLab. A stack found
on GitLab (from the stack block in the MR description, or by following target
branches) is set up locally: missing branches are created from the remote and
existing ones fast-forwarded. A branch name is looked up locally first, then
through its open MR on GitLab.

Without arguments in a terminal, pick from the local stacks and the stacks
found on GitLab.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			return app.runCheckout(arg, printPath)
		},
	}
	cmd.Flags().BoolVar(&printPath, "print-path", false, "print the path of the worktree that has the target branch checked out")
	return cmd
}

func (a *App) runCheckout(arg string, printPath bool) error {
	var target string
	err := a.mutate(func(f *stack.File) error {
		var err error
		target, err = a.resolveCheckout(f, arg)
		return err
	})
	if err != nil {
		return err
	}
	return a.goTo(target, printPath)
}

// resolveCheckout returns the branch to check out, setting up a remote stack if needed.
func (a *App) resolveCheckout(f *stack.File, arg string) (string, error) {
	if arg == "" {
		return a.pickStack(f)
	}
	if iid, ok := parseMRRef(arg); ok {
		if _, b := findLocalMR(f, iid); b != nil {
			return b.Name, nil
		}
		c, err := a.gitlab(a.defaultRemote())
		if err != nil {
			return "", err
		}
		mr, err := c.GetMR(iid)
		if err == nil {
			return a.checkoutRemote(f, c, mr)
		}
		if a.repo().ValidBranchName(arg) && !strings.HasPrefix(arg, "!") {
			return a.checkoutBranch(f, arg) // e.g. a branch named "123"
		}
		return "", err
	}
	return a.checkoutBranch(f, arg)
}

func (a *App) checkoutBranch(f *stack.File, branch string) (string, error) {
	if f.Find(branch) != nil {
		return branch, nil
	}
	c, err := a.gitlab(a.defaultRemote())
	if err != nil {
		return "", err
	}
	mr, err := c.FindMRForBranch(branch)
	if err != nil {
		return "", err
	}
	if mr == nil || !mr.Open() {
		return "", exitcode.New(exitcode.NotInStack, "%s is not in a local stack and has no open MR on GitLab", branch)
	}
	return a.checkoutRemote(f, c, mr)
}

func (a *App) checkoutRemote(f *stack.File, c *glab.Client, mr *glab.MR) (string, error) {
	info, err := remoteStackFor(c, mr)
	if err != nil {
		return "", err
	}
	if _, err := a.setupRemoteStack(f, c, info, a.defaultRemote()); err != nil {
		return "", err
	}
	return mr.SourceBranch, nil
}

func (a *App) defaultRemote() string {
	r, err := a.repo().DefaultRemote()
	if err != nil {
		return "origin"
	}
	return r
}

// stackChoice is one entry in the stack picker.
type stackChoice struct {
	label  string
	local  *stack.Stack
	remote *glab.StackInfo
}

// stackChoices lists local stacks and open stacks found on GitLab, without
// duplicates and without fully merged stacks.
func (a *App) stackChoices(f *stack.File) ([]stackChoice, *glab.Client) {
	var out []stackChoice
	tracked := map[int]bool{}
	for _, s := range f.Stacks {
		if s.Top() == nil {
			continue
		}
		for _, b := range s.Branches {
			if b.MR != nil {
				tracked[b.MR.IID] = true
			}
		}
		out = append(out, stackChoice{label: fmt.Sprintf("local   %s ← %s", s.Trunk, strings.Join(branchNames(s.Active()), " ← ")), local: s})
	}
	c, err := a.gitlab(a.defaultRemote())
	if err != nil {
		return out, nil
	}
	mrs, err := c.SearchMRs(glab.Marker)
	if err != nil {
		a.infof("Warning: could not list stacks on GitLab: %v", err)
		return out, c
	}
	for _, mr := range mrs {
		info, ok := glab.ParseBlock(mr.Description)
		if !ok || len(info.MRs) == 0 || tracked[info.MRs[0].IID] {
			continue
		}
		var parts []string
		for _, m := range info.MRs {
			tracked[m.IID] = true
			parts = append(parts, fmt.Sprintf("!%d %s", m.IID, m.Branch))
		}
		out = append(out, stackChoice{label: fmt.Sprintf("remote  %s ← %s", info.Trunk, strings.Join(parts, " ← ")), remote: info})
	}
	return out, c
}

func (a *App) pickStack(f *stack.File) (string, error) {
	if !a.Interactive {
		return "", exitcode.New(exitcode.InvalidArgs, "pass an MR number, MR URL or branch name")
	}
	choices, c := a.stackChoices(f)
	if len(choices) == 0 {
		return "", exitcode.New(exitcode.NotInStack, "no stacks found locally or on GitLab")
	}
	labels := make([]string, len(choices))
	for i, ch := range choices {
		labels[i] = ch.label
	}
	i, err := a.choose("Check out which stack?", labels)
	if err != nil {
		return "", err
	}
	ch := choices[i]
	if ch.local != nil {
		return ch.local.Top().Name, nil
	}
	s, err := a.setupRemoteStack(f, c, ch.remote, a.defaultRemote())
	if err != nil {
		return "", err
	}
	if top := s.Top(); top != nil {
		return top.Name, nil
	}
	return s.Trunk, nil
}
