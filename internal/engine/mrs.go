package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// SyncMRs refreshes each unmerged layer's MR from GitLab and marks merged
// layers. It returns the MRs found, keyed by branch. Merged layers are not
// fetched again: merged is final on GitLab.
//
// An MR found by branch name alone (no MR recorded, or the recorded one was
// closed) is only taken if it is open, or if it merged exactly what the local
// branch holds: an old merged MR whose source branch merely had the same name
// must not make a new layer count as merged.
func SyncMRs(c *glab.Client, g *git.Repo, s *stack.Stack) (map[string]*glab.MR, error) {
	found := map[string]*glab.MR{}
	var errs []error
	for _, b := range s.Branches {
		if b.Merged {
			continue
		}
		var mr *glab.MR
		var err error
		if b.MR != nil {
			mr, err = c.GetMR(b.MR.IID)
			// An MR of another branch: the IID was recorded in another project
			// (e.g. one reached through --remote).
			if err == nil && mr.SourceBranch != b.Name {
				mr = nil
			}
			// A closed MR may have been replaced by a newer one for the same branch.
			if err == nil && (mr == nil || mr.State == "closed") {
				if newer, ferr := c.FindMRForBranch(b.Name); ferr == nil && newer != nil && belongs(g, b.Name, newer) {
					mr = newer
				}
			}
		} else {
			mr, err = c.FindMRForBranch(b.Name)
			if err == nil && mr != nil && !belongs(g, b.Name, mr) {
				mr = nil
			}
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

// belongs reports whether mr, found by source branch name, is the MR of the
// local branch: an open MR is, a merged or closed one only if the local tip is
// contained in the commit it ended on.
func belongs(g *git.Repo, branch string, mr *glab.MR) bool {
	if mr.Open() {
		return true
	}
	return mr.SHA != "" && g.IsAncestor(git.Ref(branch), mr.SHA)
}

// Push pushes branches to remote in one `git push`. Each branch carries an
// explicit --force-with-lease on the remote-tracking value, and is only pushed
// if that value was integrated locally (git.Repo.Includes): a commit someone
// else pushed, such as an applied review suggestion, is refused rather than
// overwritten, even right after a fetch has refreshed the tracking refs.
func Push(g *git.Repo, remote string, branches []string) error {
	if len(branches) == 0 {
		return nil
	}
	args := []string{"push", "--quiet", "--porcelain"}
	var refspecs, foreign []string
	for _, b := range branches {
		lease := ""
		if sha, err := g.RevParse("refs/remotes/" + remote + "/" + b); err == nil {
			if !g.Includes(b, sha) {
				foreign = append(foreign, b)
				continue
			}
			lease = sha
		}
		args = append(args, "--force-with-lease="+git.Ref(b)+":"+lease)
		refspecs = append(refspecs, git.Ref(b)+":"+git.Ref(b))
	}
	if len(foreign) > 0 {
		return exitcode.New(exitcode.Generic, "nothing was pushed: %s on %s holds commits that your local branch never had; integrate them (e.g. `git pull --rebase`), then try again",
			strings.Join(foreign, ", "), remote)
	}
	args = append(append(args, remote), refspecs...)
	// RunEnv keeps stdout in the error: --porcelain reports rejections there.
	if _, err := g.RunEnv(nil, args...); err != nil {
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
		if b.MR == nil || b.Merged {
			continue // merged MRs are no longer open
		}
		mr := known[b.Name]
		if mr == nil || mr.IID != b.MR.IID {
			var err error
			if mr, err = c.GetMR(b.MR.IID); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if !mr.Open() || mr.SourceBranch != b.Name {
			continue // closed, or another branch's MR (see SyncMRs)
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
	out, err := g.Run("log", "--reverse", "--format=%s%x00%b%x01", git.Ref(parent)+".."+git.Ref(branch))
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
