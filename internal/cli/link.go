package cli

import (
	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
)

type linkOptions struct {
	base, remote string
	open         bool
}

func newLinkCmd(app *App) *cobra.Command {
	var o linkOptions
	cmd := &cobra.Command{
		Use:   "link <branch-or-mr> [...]",
		Short: "Link branches or MRs into a stack on GitLab",
		Long: `Create or extend a stack on GitLab from branches or MR numbers/URLs, given
bottom to top. Local stack tracking is not read or changed.

Branches are pushed first. Branches without an MR get one, targeting the item
below. Existing MRs with the wrong target branch are retargeted. Linking only
adds: MRs already in the stack are never removed from it.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return app.runLink(o, args)
		},
	}
	cmd.Flags().StringVar(&o.base, "base", "", "target branch of the bottom item (default: the remote's default branch)")
	cmd.Flags().BoolVar(&o.open, "open", false, "create new MRs as ready for review and mark drafts ready")
	cmd.Flags().StringVar(&o.remote, "remote", "", "remote to push to (default: auto-detected)")
	return cmd
}

func (a *App) runLink(o linkOptions, args []string) error {
	g := a.repo()
	remote := o.remote
	if remote == "" {
		remote = a.defaultRemote()
	}
	base := o.base
	if base == "" {
		var err error
		if base, err = g.DefaultBranch(remote); err != nil {
			return exitcode.Wrap(exitcode.InvalidArgs, err)
		}
	}
	c, err := a.gitlab(remote)
	if err != nil {
		return err
	}

	// Resolve every argument to a branch, and an MR when one exists.
	type item struct {
		branch string
		mr     *glab.MR
	}
	var items []item
	var toPush []string
	for _, arg := range args {
		if iid, ok := parseMRRef(arg); ok && !g.BranchExists(arg) {
			mr, err := c.GetMR(iid)
			if err != nil {
				return err
			}
			items = append(items, item{mr.SourceBranch, mr})
			continue
		}
		if !g.BranchExists(arg) {
			return exitcode.New(exitcode.InvalidArgs, "%q is neither a local branch nor an MR", arg)
		}
		items = append(items, item{branch: arg})
		toPush = append(toPush, arg)
	}
	if err := engine.Push(g, remote, toPush); err != nil {
		return err
	}

	target := base
	for i := range items {
		it := &items[i]
		if it.mr == nil {
			if it.mr, err = c.FindMRForBranch(it.branch); err != nil {
				return err
			}
		}
		switch {
		case it.mr == nil || !it.mr.Open():
			title, body := engine.DefaultTitleBody(g, target, it.branch)
			if it.mr, err = c.CreateMR(glab.NewMR{Source: it.branch, Target: target, Title: title, Description: body, Draft: !o.open}); err != nil {
				return err
			}
			a.infof("Created !%d for %s", it.mr.IID, it.branch)
		case it.mr.TargetBranch != target:
			if it.mr, err = c.UpdateMR(it.mr.IID, map[string]any{"target_branch": target}); err != nil {
				return err
			}
			a.infof("Retargeted !%d to %s", it.mr.IID, target)
		}
		if o.open && it.mr.Draft {
			if it.mr, err = c.MarkReady(it.mr); err != nil {
				return err
			}
		}
		target = it.branch
	}

	// Merge into any existing stack: keep its MRs, insert ours in order.
	info := glab.StackInfo{Trunk: base}
	for _, it := range items {
		if existing, ok := glab.ParseBlock(it.mr.Description); ok {
			info = *existing
			break
		}
	}
	pos := -1
	for _, it := range items {
		idx := -1
		for j, m := range info.MRs {
			if m.IID == it.mr.IID {
				idx = j
			}
		}
		if idx < 0 {
			idx = pos + 1
			entry := glab.StackItem{IID: it.mr.IID, Branch: it.branch}
			info.MRs = append(info.MRs[:idx], append([]glab.StackItem{entry}, info.MRs[idx:]...)...)
		}
		pos = idx
	}
	for _, m := range info.MRs {
		mr, err := c.GetMR(m.IID)
		if err != nil {
			return err
		}
		if !mr.Open() {
			continue
		}
		desc := glab.SetBlock(mr.Description, glab.RenderBlock(info, mr.IID))
		if desc != mr.Description {
			if _, err := c.UpdateMR(mr.IID, map[string]any{"description": desc}); err != nil {
				return err
			}
		}
	}
	a.infof("Linked %d MR(s) into a stack on %s", len(info.MRs), info.Trunk)
	return nil
}
