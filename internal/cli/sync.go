package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

type syncOptions struct {
	remote string
	prune  bool
}

func newSyncCmd(app *App) *cobra.Command {
	var o syncOptions
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync the current stack with the remote",
		Long: `Fetch, rebase, push, and sync merge request state for the current stack.

  1. Fetches the latest changes from the remote
  2. Reconciles the stack on GitLab (the stack block in the MR descriptions)
     with your local stack: layers added on GitLab are pulled down
     automatically; if the two have diverged you are asked what to do
     (outside a terminal, sync aborts)
  3. Fast-forwards the trunk to match the remote
  4. Cascade-rebases the stack if the trunk moved; on conflict every branch
     is restored and you are told to run "glab stax rebase"
  5. Pushes all unmerged branches with --force-with-lease
  6. Syncs MR state, retargeting MRs whose lower layer was merged
  7. Updates the stack block in every MR when two or more MRs exist

Sync never opens merge requests; use "glab stax submit" for that.
With --prune, local branches of merged MRs are deleted without asking.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runSync(o)
		},
	}
	cmd.Flags().StringVar(&o.remote, "remote", "", "remote to sync with (default: the stack's remote)")
	cmd.Flags().BoolVar(&o.prune, "prune", false, "delete local branches of merged MRs without asking")
	return cmd
}

func (a *App) runSync(o syncOptions) error {
	return a.mutate(func(f *stack.File) error {
		s, cur, err := a.currentStack(f)
		if err != nil {
			return err
		}
		remote := pickRemote(o.remote, s.Remote)
		c, err := a.gitlab(remote)
		if err != nil {
			return err
		}
		g := a.repo()
		if err := g.Fetch(remote); err != nil {
			return err
		}
		known, err := engine.SyncMRs(c, s)
		if err != nil {
			return err
		}
		if err := a.reconcile(f, s, c, known, remote); err != nil {
			return err
		}

		st, err := a.store()
		if err != nil {
			return err
		}
		r := &engine.Rebaser{Repo: g, Store: st, Log: a.infof}
		trunkSHA, err := r.UpdateFromRemote(s, remote)
		if err != nil {
			return err
		}
		if bottom := s.Bottom(); bottom != nil && !g.IsAncestor(trunkSHA, bottom.Name) {
			if err := r.Start(s, engine.RebaseOptions{Anchor: cur, Remote: remote, AbortOnConflict: true}); err != nil {
				return err
			}
		}

		if active := branchNames(s.Active()); len(active) > 0 {
			if err := engine.Push(g, remote, active); err != nil {
				return err
			}
			a.infof("Pushed %d branch(es) to %s", len(active), remote)
		}

		if err := a.retargetAndReport(c, s, known); err != nil {
			return err
		}
		withMR := 0
		for _, b := range s.Branches {
			if b.MR != nil && known[b.Name] != nil && known[b.Name].Open() {
				withMR++
			}
		}
		if withMR >= 2 {
			if err := engine.UpdateBlocks(c, s, known); err != nil {
				return err
			}
			a.infof("Stack synced")
		} else {
			a.infof("Branches synced")
		}
		return a.pruneMerged(s, o.prune)
	})
}

// reconcile brings the local stack in line with the stack recorded on GitLab.
func (a *App) reconcile(f *stack.File, s *stack.Stack, c *glab.Client, known map[string]*glab.MR, remote string) error {
	info := engine.RemoteStack(s, known)
	switch engine.Compare(s, info) {
	case engine.RemoteAhead:
		have := 0
		for _, b := range s.Branches {
			if b.MR != nil {
				have++
			}
		}
		for _, item := range info.MRs[have:] {
			if err := a.appendRemoteLayer(f, s, c, known, item, remote); err != nil {
				return err
			}
			a.infof("Pulled %s (!%d) from GitLab", item.Branch, item.IID)
		}
	case engine.Diverged:
		if !a.Interactive {
			return exitcode.New(exitcode.Generic, "the local stack and the stack on GitLab have diverged; run sync in a terminal to resolve it")
		}
		choice, err := a.choose("The local stack and the stack on GitLab have diverged.", []string{
			"Use the stack on GitLab (local layers not on GitLab leave the stack)",
			"Keep the local stack (GitLab is updated to match)",
			"Cancel",
		})
		if err != nil {
			return err
		}
		switch choice {
		case 0:
			return a.adoptRemote(f, s, c, known, info, remote)
		case 1:
			return a.detachRemoteOnly(s, c, info)
		default:
			return exitcode.New(exitcode.Generic, "sync cancelled")
		}
	}
	return nil
}

// appendRemoteLayer adds a layer that exists on GitLab on top of s.
func (a *App) appendRemoteLayer(f *stack.File, s *stack.Stack, c *glab.Client, known map[string]*glab.MR, item glab.StackItem, remote string) error {
	if other := f.Find(item.Branch); other != nil && other != s {
		return exitcode.New(exitcode.Ambiguous, "%s is already part of another local stack", item.Branch)
	}
	b, err := a.layerFromRemote(s, item, remote)
	if err != nil {
		return err
	}
	if mr, err := c.GetMR(item.IID); err == nil {
		known[item.Branch] = mr
		b.MR = &stack.MR{IID: mr.IID, URL: mr.WebURL}
		b.Merged = mr.Merged()
	}
	s.Branches = append(s.Branches, b)
	return nil
}

// layerFromRemote returns a branch record for item, creating the local branch
// from the remote one if needed. Its base is where it forks from the current top.
func (a *App) layerFromRemote(s *stack.Stack, item glab.StackItem, remote string) (*stack.Branch, error) {
	g := a.repo()
	if !g.BranchExists(item.Branch) {
		if _, err := g.Run("branch", "--track", item.Branch, remote+"/"+item.Branch); err != nil {
			return nil, fmt.Errorf("cannot create %s from %s: %w", item.Branch, remote, err)
		}
	}
	below := s.Trunk
	if len(s.Branches) > 0 {
		below = s.Branches[len(s.Branches)-1].Name
	}
	base, err := g.MergeBase(below, item.Branch)
	if err != nil {
		return nil, err
	}
	return &stack.Branch{Name: item.Branch, Base: base, MR: &stack.MR{IID: item.IID}}, nil
}

// adoptRemote replaces the local layer list with GitLab's.
func (a *App) adoptRemote(f *stack.File, s *stack.Stack, c *glab.Client, known map[string]*glab.MR, info *glab.StackInfo, remote string) error {
	if clean, err := a.repo().IsClean(); err != nil || !clean {
		return exitcode.New(exitcode.Generic, "commit or stash your changes before adopting the stack from GitLab")
	}
	old := s.Branches
	s.Branches = nil
	for _, item := range info.MRs {
		var b *stack.Branch
		for _, ob := range old {
			if ob.Name == item.Branch {
				b = ob
			}
		}
		if b == nil {
			if err := a.appendRemoteLayer(f, s, c, known, item, remote); err != nil {
				return err
			}
			continue
		}
		s.Branches = append(s.Branches, b)
	}
	a.infof("Local stack now matches GitLab: %v", branchNames(s.Branches))
	return nil
}

// detachRemoteOnly removes the stack block from MRs GitLab lists but the local stack does not.
func (a *App) detachRemoteOnly(s *stack.Stack, c *glab.Client, info *glab.StackInfo) error {
	for _, item := range info.MRs {
		if b := s.Get(item.Branch); b != nil && b.MR != nil && b.MR.IID == item.IID {
			continue
		}
		mr, err := c.GetMR(item.IID)
		if err != nil {
			return err
		}
		if desc := glab.StripBlock(mr.Description); desc != mr.Description {
			if _, err := c.UpdateMR(mr.IID, map[string]any{"description": desc}); err != nil {
				return err
			}
			a.infof("Removed !%d from the stack on GitLab", mr.IID)
		}
	}
	return nil
}

// retargetAndReport points each open MR at the layer below it and prints MR state.
func (a *App) retargetAndReport(c *glab.Client, s *stack.Stack, known map[string]*glab.MR) error {
	for _, b := range s.Branches {
		mr := known[b.Name]
		switch {
		case mr == nil:
			if !b.Merged {
				a.infof("  %s: no MR (run `glab stax submit`)", b.Name)
			}
			continue
		case mr.Merged():
			a.infof("  !%d %s: merged", mr.IID, b.Name)
			continue
		case !mr.Open():
			a.infof("  !%d %s: %s", mr.IID, b.Name, mr.State)
			continue
		}
		if target := s.Parent(b.Name); mr.TargetBranch != target {
			updated, err := c.UpdateMR(mr.IID, map[string]any{"target_branch": target})
			if err != nil {
				return err
			}
			known[b.Name], mr = updated, updated
			a.infof("Retargeted !%d to %s", mr.IID, target)
		}
		state := "open"
		if mr.Draft {
			state = "draft"
		}
		a.infof("  !%d %s: %s", mr.IID, b.Name, state)
	}
	return nil
}

// pruneMerged deletes local branches of merged layers, asking first unless force.
func (a *App) pruneMerged(s *stack.Stack, force bool) error {
	g := a.repo()
	var doomed []string
	for _, b := range s.Branches {
		if b.Merged && g.BranchExists(b.Name) {
			doomed = append(doomed, b.Name)
		}
	}
	if len(doomed) == 0 {
		return nil
	}
	if !force {
		if !a.Interactive {
			return nil
		}
		ok, err := a.confirm(fmt.Sprintf("Delete %d local branch(es) of merged MRs (%v)?", len(doomed), doomed), false)
		if err != nil || !ok {
			return err
		}
	}
	cur, err := g.CurrentBranch()
	if err != nil {
		return err
	}
	for _, name := range doomed {
		owner, err := a.foreignOwner(name)
		if err != nil {
			return err
		}
		if owner != "" {
			a.infof("Kept %s: it is checked out in %s", name, owner)
			continue
		}
		if name == cur {
			dest := s.Trunk
			if bottom := s.Bottom(); bottom != nil {
				dest = bottom.Name
			}
			if err := g.Checkout(dest); err != nil {
				return err
			}
			a.infof("Switched to %s", dest)
			cur = dest
		}
		if _, err := g.Run("branch", "-D", name); err != nil {
			return err
		}
		a.infof("Deleted %s", name)
	}
	return nil
}
