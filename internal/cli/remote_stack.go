package cli

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

const maxChain = 50

var mrURLRe = regexp.MustCompile(`/-/merge_requests/(\d+)`)

// parseMRRef recognises "123", "!123" and MR URLs.
func parseMRRef(arg string) (int, bool) {
	if m := mrURLRe.FindStringSubmatch(arg); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "!"))
	return n, err == nil && n > 0
}

// findLocalMR returns the local stack and branch that track MR iid.
func findLocalMR(f *stack.File, iid int) (*stack.Stack, *stack.Branch) {
	for _, s := range f.Stacks {
		for _, b := range s.Branches {
			if b.MR != nil && b.MR.IID == iid {
				return s, b
			}
		}
	}
	return nil, nil
}

// remoteStackFor returns the stack containing mr, from its description block
// or, failing that, by following target branches through open MRs.
func remoteStackFor(c *glab.Client, mr *glab.MR) (*glab.StackInfo, error) {
	if info, ok := glab.ParseBlock(mr.Description); ok {
		return info, nil
	}
	chain := []*glab.MR{mr}
	for len(chain) < maxChain {
		below, err := c.FindMRForBranch(chain[0].TargetBranch)
		if err != nil {
			return nil, err
		}
		if below == nil || !below.Open() {
			break
		}
		chain = append([]*glab.MR{below}, chain...)
	}
	for len(chain) < maxChain {
		above, err := c.ListMRsTargeting(chain[len(chain)-1].SourceBranch)
		if err != nil {
			return nil, err
		}
		if len(above) != 1 {
			break // none, or a fork we cannot order
		}
		chain = append(chain, above[0])
	}
	info := &glab.StackInfo{Trunk: chain[0].TargetBranch}
	for _, m := range chain {
		info.MRs = append(info.MRs, glab.StackItem{IID: m.IID, Branch: m.SourceBranch})
	}
	return info, nil
}

// setupRemoteStack makes a local stack for info, creating branches from the
// remote as needed. An existing local stack with the same layers is reused.
func (a *App) setupRemoteStack(f *stack.File, c *glab.Client, info *glab.StackInfo, remote string) (*stack.Stack, error) {
	g := a.repo()
	if err := g.Fetch(remote); err != nil {
		return nil, err
	}
	var existing *stack.Stack
	for _, item := range info.MRs {
		if s := f.Find(item.Branch); s != nil {
			if existing != nil && existing != s {
				return nil, exitcode.New(exitcode.Ambiguous, "the stack's branches belong to several local stacks; run `glab stax unstack --local` first")
			}
			existing = s
		}
	}
	if existing != nil {
		if engine.Compare(existing, info) == engine.InSync || engine.Compare(existing, info) == engine.LocalAhead {
			return existing, nil
		}
		if !a.Interactive {
			return nil, exitcode.New(exitcode.Ambiguous, "a local stack with some of these branches differs from the stack on GitLab; run in a terminal to replace it")
		}
		ok, err := a.confirm("A local stack with some of these branches differs from the one on GitLab. Replace it with GitLab's?", false)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, exitcode.New(exitcode.Ambiguous, "checkout cancelled")
		}
		f.Remove(existing)
	}
	if err := a.ensureLocalBranch(remote, info.Trunk); err != nil {
		return nil, err
	}
	s := &stack.Stack{Trunk: info.Trunk, Remote: remote}
	for _, item := range info.MRs {
		mr, err := c.GetMR(item.IID)
		if err != nil {
			return nil, err
		}
		b := &stack.Branch{Name: item.Branch, MR: &stack.MR{IID: mr.IID, URL: mr.WebURL}, Merged: mr.Merged()}
		if err := a.syncLocalBranch(remote, item.Branch, b.Merged); err != nil {
			return nil, err
		}
		if g.BranchExists(item.Branch) {
			below := s.Trunk
			if len(s.Branches) > 0 {
				below = s.Branches[len(s.Branches)-1].Name
			}
			if g.BranchExists(below) {
				b.Base, _ = g.MergeBase(below, item.Branch)
			}
		}
		s.Branches = append(s.Branches, b)
	}
	f.Stacks = append(f.Stacks, s)
	if err := g.EnableRerere(); err != nil {
		return nil, err
	}
	a.infof("Set up stack on %s: %s", s.Trunk, strings.Join(branchNames(s.Branches), " → "))
	return s, nil
}

// syncLocalBranch creates branch from the remote, or fast-forwards it. A
// diverged local branch is kept as is. Merged branches deleted upstream are skipped.
func (a *App) syncLocalBranch(remote, branch string, merged bool) error {
	g := a.repo()
	remoteSHA, err := g.RevParse("refs/remotes/" + remote + "/" + branch)
	if err != nil {
		if merged || g.BranchExists(branch) {
			return nil
		}
		return fmt.Errorf("branch %s does not exist on %s", branch, remote)
	}
	if !g.BranchExists(branch) {
		_, err := g.Run("branch", "--track", branch, remote+"/"+branch)
		return err
	}
	local, err := g.RevParse("refs/heads/" + branch)
	if err != nil || local == remoteSHA {
		return err
	}
	if !g.IsAncestor(local, remoteSHA) {
		a.infof("Warning: local %s has diverged from %s; keeping the local branch", branch, remote)
		return nil
	}
	owner, err := g.WorktreeFor(branch)
	if err != nil {
		return err
	}
	if owner != "" {
		_, err = g.At(owner).Run("merge", "--ff-only", "--quiet", remoteSHA)
	} else {
		err = g.SetBranchSHA(branch, remoteSHA)
	}
	return err
}
