package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

type submitOptions struct {
	auto, open bool
	remote     string
}

func newSubmitCmd(app *App) *cobra.Command {
	var o submitOptions
	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Push the stack and create or update its merge requests",
		Long: `Push every unmerged branch and create or update one merge request per
branch, each targeting the branch below it. Every MR in the stack gets a
block in its description that lists the whole stack.

Steps:
  1. Pushes all branches to the remote
  2. Creates MRs for branches that have none
  3. Fixes target branches of existing MRs
  4. Updates the stack block in every MR description

With --auto (or outside a terminal), titles come from the commits and new MRs
are created as drafts unless --open is passed. --open also marks existing
draft MRs as ready.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.runSubmit(o)
		},
	}
	cmd.Flags().BoolVar(&o.auto, "auto", false, "use generated titles without prompting")
	cmd.Flags().BoolVar(&o.open, "open", false, "mark new and existing MRs as ready for review")
	cmd.Flags().StringVar(&o.remote, "remote", "", "remote to push to (default: the stack's remote)")
	return cmd
}

func (a *App) runSubmit(o submitOptions) error {
	return a.mutate(func(f *stack.File) error {
		s, _, err := a.currentStack(f)
		if err != nil {
			return err
		}
		remote := pickRemote(o.remote, s.Remote)
		c, err := a.gitlab(remote)
		if err != nil {
			return err
		}
		known, err := engine.SyncMRs(c, s)
		if err != nil {
			return err
		}
		pruneFullyMerged(s)
		active := s.Active()
		if len(active) == 0 {
			a.infof("All branches are merged; nothing to submit")
			return nil
		}
		g := a.repo()
		_ = g.Fetch(remote) // refresh tracking refs for --force-with-lease; may fail for new remotes
		if err := engine.Push(g, remote, branchNames(active)); err != nil {
			return err
		}
		a.infof("Pushed %d branch(es) to %s", len(active), remote)

		var errs []error
		for _, b := range active {
			if err := a.ensureMR(c, s, b, known, o); err != nil {
				a.infof("Warning: %s: %v", b.Name, err)
				errs = append(errs, err)
			}
		}
		if err := engine.UpdateBlocks(c, s, known); err != nil {
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			// Keep what was created: save the metadata, then report failure.
			if serr := a.saveNow(f); serr != nil {
				errs = append(errs, serr)
			}
			return exitcode.Wrap(exitcode.GitLabFailure, errors.Join(errs...))
		}
		return nil
	})
}

// pruneFullyMerged drops merged layers once no MR in the stack is still open,
// so new layers start a fresh stack on the trunk.
func pruneFullyMerged(s *stack.Stack) {
	for _, b := range s.Active() {
		if b.MR != nil {
			return
		}
	}
	s.Branches = s.Active()
}

func (a *App) ensureMR(c *glab.Client, s *stack.Stack, b *stack.Branch, known map[string]*glab.MR, o submitOptions) error {
	target := s.Parent(b.Name)
	mr := known[b.Name]
	if mr == nil || !mr.Open() {
		return a.createMR(c, b, target, known, o)
	}
	if mr.AutoMerge {
		if err := c.CancelAutoMerge(mr.IID); err != nil {
			return err
		}
		a.infof("Disabled auto-merge on !%d (incompatible with stacked MRs)", mr.IID)
	}
	if mr.TargetBranch != target {
		updated, err := c.UpdateMR(mr.IID, map[string]any{"target_branch": target})
		if err != nil {
			return err
		}
		known[b.Name] = updated
		a.infof("Retargeted !%d to %s", mr.IID, target)
	} else {
		a.infof("!%d for %s is up to date", mr.IID, b.Name)
	}
	if o.open && known[b.Name].Draft {
		updated, err := c.MarkReady(known[b.Name])
		if err != nil {
			return err
		}
		known[b.Name] = updated
		a.infof("Marked !%d as ready", mr.IID)
	}
	return nil
}

func (a *App) createMR(c *glab.Client, b *stack.Branch, target string, known map[string]*glab.MR, o submitOptions) error {
	title, body := engine.DefaultTitleBody(a.repo(), target, b.Name)
	if tmpl := a.mrTemplate(); tmpl != "" {
		body = tmpl
	}
	draft := !o.open
	if a.Interactive && !o.auto {
		answer, err := a.input(fmt.Sprintf("Title for the %s MR [%s]:", b.Name, title))
		if err != nil {
			return err
		}
		if answer != "" {
			title = answer
		}
		draft = false
	}
	mr, err := c.CreateMR(glab.NewMR{Source: b.Name, Target: target, Title: title, Description: body, Draft: draft})
	if err != nil {
		return err
	}
	known[b.Name] = mr
	b.MR = &stack.MR{IID: mr.IID, URL: mr.WebURL}
	a.infof("Created !%d for %s: %s", mr.IID, b.Name, mr.WebURL)
	return nil
}

// mrTemplate returns the project's default merge request template, if any.
func (a *App) mrTemplate() string {
	top, err := a.repo().TopLevel()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(top, ".gitlab", "merge_request_templates", "Default.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// saveNow persists f mid-command, for partial successes that still return an error.
func (a *App) saveNow(f *stack.File) error {
	st, err := a.store()
	if err != nil {
		return err
	}
	return st.Save(f)
}
