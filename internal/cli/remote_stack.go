package cli

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/engine"
	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/git"
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

const maxChain = 50

var mrURLRe = regexp.MustCompile(`/-/merge_requests/(\d+)`)

// mrRef is a parsed MR reference.
type mrRef struct {
	iid     int
	project string // project path of an MR URL; "" for "123" and "!123"
}

// parseMRRef recognises "123", "!123" and MR URLs.
func parseMRRef(arg string) (mrRef, bool) {
	if m := mrURLRe.FindStringSubmatch(arg); m != nil {
		n, err := strconv.Atoi(m[1])
		ref := mrRef{iid: n}
		if u, perr := url.Parse(arg); perr == nil && u.Host != "" {
			ref.project, _, _ = strings.Cut(strings.Trim(u.Path, "/"), "/-/merge_requests/")
		}
		return ref, err == nil && n > 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "!"))
	return mrRef{iid: n}, err == nil && n > 0
}

// check fails if the reference is a URL of another project than c's: its
// number would otherwise be applied to a different MR of this project.
func (r mrRef) check(c *glab.Client) error {
	if r.project != "" && !strings.EqualFold(r.project, c.Project.Path) {
		return exitcode.New(exitcode.InvalidArgs, "that MR belongs to %s, but this repository's project is %s", r.project, c.Project.Path)
	}
	return nil
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
// or, failing that, by following target branches through open MRs. The walk
// down stops at a protected or default branch: a branch like main with its
// own release MR is a trunk, not a layer.
func remoteStackFor(c *glab.Client, mr *glab.MR) (*glab.StackInfo, error) {
	if info, ok := glab.ParseBlock(mr.Description); ok {
		return info, nil
	}
	chain := []*glab.MR{mr}
	seen := map[int]bool{mr.IID: true}
	for len(chain) < maxChain {
		target := chain[0].TargetBranch
		if b, err := c.GetBranch(target); err == nil && (b.Protected || b.Default) {
			break
		}
		below, err := c.FindMRForBranch(target)
		if err != nil {
			return nil, err
		}
		if below == nil || !below.Open() || seen[below.IID] {
			break
		}
		seen[below.IID] = true
		chain = append([]*glab.MR{below}, chain...)
	}
	for len(chain) < maxChain {
		above, err := c.ListMRsTargeting(chain[len(chain)-1].SourceBranch)
		if err != nil {
			return nil, err
		}
		if len(above) != 1 || seen[above[0].IID] {
			break // none, several we cannot order, or a loop
		}
		seen[above[0].IID] = true
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
		if rel := engine.Compare(existing, info); rel == engine.InSync || rel == engine.LocalAhead {
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
	below := git.Ref(info.Trunk) // where the next layer's own commits start
	for _, item := range info.MRs {
		mr, err := c.GetMR(item.IID)
		if err != nil {
			return nil, err
		}
		b := &stack.Branch{Name: item.Branch, MR: &stack.MR{IID: mr.IID, URL: mr.WebURL}, Merged: mr.Merged()}
		if err := a.syncLocalBranch(remote, item.Branch, b.Merged); err != nil {
			return nil, err
		}
		b.Base = a.forkBase(below, item.Branch)
		below = layerTip(g, item.Branch, mr)
		s.Branches = append(s.Branches, b)
	}
	f.Stacks = append(f.Stacks, s)
	if err := g.EnableRerere(); err != nil {
		return nil, err
	}
	a.infof("Set up stack on %s: %s", s.Trunk, strings.Join(branchNames(s.Branches), " → "))
	return s, nil
}

// forkBase returns where branch forks from below (a revision), or "" if
// either is unknown locally.
func (a *App) forkBase(below, branch string) string {
	g := a.repo()
	if below == "" || !g.BranchExists(branch) {
		return ""
	}
	base, _ := g.MergeBase(below, git.Ref(branch))
	return base
}

// layerTip returns a revision for a layer's tip: its local branch or, for a
// merged layer whose branch is gone, the commit its MR ended on. The layer
// above starts there, and rebases need that cut point.
func layerTip(g *git.Repo, branch string, mr *glab.MR) string {
	if g.BranchExists(branch) {
		return git.Ref(branch)
	}
	if mr != nil && mr.SHA != "" {
		return mr.SHA
	}
	return ""
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
	local, err := g.RevParse(git.Ref(branch))
	if err != nil || local == remoteSHA {
		return err
	}
	if !g.IsAncestor(local, remoteSHA) {
		a.infof("Warning: local %s has diverged from %s; keeping the local branch", branch, remote)
		return nil
	}
	if g.Includes(branch, remoteSHA) {
		return nil // moved back from the remote tip on purpose
	}
	owner, err := g.WorktreeFor(branch)
	if err != nil {
		return err
	}
	if owner != "" {
		_, err = g.At(owner).Run("merge", "--ff-only", "--quiet", remoteSHA)
	} else {
		_, err = g.Run("update-ref", git.Ref(branch), remoteSHA, local)
	}
	return err
}
