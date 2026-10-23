package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// SyncMRs refreshes each layer's MR from GitLab and marks merged layers.
// It returns the MRs found, keyed by branch.
func SyncMRs(c *glab.Client, s *stack.Stack) (map[string]*glab.MR, error) {
	found := map[string]*glab.MR{}
	var errs []error
	for _, b := range s.Branches {
		var mr *glab.MR
		var err error
		if b.MR != nil {
			mr, err = c.GetMR(b.MR.IID)
			// A closed MR may have been replaced by a newer one for the same branch.
			if err == nil && mr.State == "closed" {
				if newer, ferr := c.FindMRForBranch(b.Name); ferr == nil && newer != nil {
					mr = newer
				}
			}
		} else if !b.Merged {
			mr, err = c.FindMRForBranch(b.Name)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if mr == nil {
			continue
		}
		found[b.Name] = mr
		b.MR = &stack.MR{IID: mr.IID, URL: mr.WebURL}
		if mr.Merged() {
			b.Merged = true
		}
	}
	return found, errors.Join(errs...)
}

// Push pushes branches to remote in one `git push`, each with --force-with-lease.
func Push(g *git.Repo, remote string, branches []string) error {
	if len(branches) == 0 {
		return nil
	}
	args := []string{"push", "--quiet", "--porcelain", "--force-with-lease", remote}
	for _, b := range branches {
		args = append(args, "refs/heads/"+b+":refs/heads/"+b)
	}
	if _, err := g.Run(args...); err != nil {
		return fmt.Errorf("push to %s failed: %w", remote, err)
	}
	return nil
}

// StackInfo builds the description-block data for s from the layers that have MRs.
func StackInfo(s *stack.Stack) glab.StackInfo {
	info := glab.StackInfo{Trunk: s.Trunk}
	for _, b := range s.Branches {
		if b.MR != nil {
			info.MRs = append(info.MRs, glab.StackItem{IID: b.MR.IID, Branch: b.Name})
		}
	}
	return info
}

// UpdateBlocks rewrites the stack block in every open MR of s that needs it.
func UpdateBlocks(c *glab.Client, s *stack.Stack, known map[string]*glab.MR) error {
	info := StackInfo(s)
	var errs []error
	for _, b := range s.Branches {
		if b.MR == nil {
			continue
		}
		mr := known[b.Name]
		if mr == nil || mr.IID != b.MR.IID {
			var err error
			if mr, err = c.GetMR(b.MR.IID); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if !mr.Open() {
			continue
		}
		desc := glab.SetBlock(mr.Description, glab.RenderBlock(info, mr.IID))
		if desc == mr.Description {
			continue
		}
		updated, err := c.UpdateMR(mr.IID, map[string]any{"description": desc})
		if err != nil {
			errs = append(errs, err)
			continue
		}
		known[b.Name] = updated
	}
	return errors.Join(errs...)
}

// DefaultTitleBody derives an MR title and description from a layer's commits:
// a single commit gives its subject and body, otherwise the humanized branch name.
func DefaultTitleBody(g *git.Repo, parent, branch string) (string, string) {
	out, err := g.Run("log", "--reverse", "--format=%s%x00%b%x01", parent+".."+branch)
	if err == nil {
		commits := strings.Split(strings.TrimSuffix(strings.TrimSpace(out), "\x01"), "\x01")
		if len(commits) == 1 && commits[0] != "" {
			subject, body, _ := strings.Cut(strings.TrimSpace(commits[0]), "\x00")
			return subject, strings.TrimSpace(body)
		}
	}
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' {
			return ' '
		}
		return r
	}, branch), ""
}
