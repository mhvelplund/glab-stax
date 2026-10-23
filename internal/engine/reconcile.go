package engine

import (
	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

// Relation describes how the stack on GitLab compares to the local stack.
type Relation int

const (
	NoRemoteStack Relation = iota // no MR carries a stack block
	InSync                        // same layers
	LocalAhead                    // local has extra layers on top (submit will add them)
	RemoteAhead                   // GitLab has extra layers on top
	Diverged                      // anything else
)

func (r Relation) String() string {
	return [...]string{"no remote stack", "in sync", "local ahead", "remote ahead", "diverged"}[r]
}

// RemoteStack returns the stack block from the first open MR of s.
func RemoteStack(s *stack.Stack, known map[string]*glab.MR) *glab.StackInfo {
	for _, b := range s.Branches {
		mr := known[b.Name]
		if mr == nil || !mr.Open() {
			continue
		}
		if info, ok := glab.ParseBlock(mr.Description); ok {
			return info
		}
	}
	return nil
}

// Compare relates the local stack to the remote one. Only local layers with
// an MR are compared: layers without one are local additions that submit
// will publish, unless GitLab has extra layers that would have to go above them.
func Compare(s *stack.Stack, remote *glab.StackInfo) Relation {
	if remote == nil {
		return NoRemoteStack
	}
	var local []string
	unsubmittedTop := false
	for _, b := range s.Branches {
		if b.MR == nil {
			unsubmittedTop = true
			continue
		}
		unsubmittedTop = false
		local = append(local, b.Name)
	}
	rem := make([]string, len(remote.MRs))
	for i, m := range remote.MRs {
		rem[i] = m.Branch
	}
	switch {
	case equal(local, rem):
		return InSync
	case isPrefix(rem, local):
		return LocalAhead
	case isPrefix(local, rem) && !unsubmittedTop:
		return RemoteAhead
	default:
		return Diverged
	}
}

func equal(a, b []string) bool { return len(a) == len(b) && isPrefix(a, b) }

// isPrefix reports whether p is a prefix of s.
func isPrefix(p, s []string) bool {
	if len(p) > len(s) {
		return false
	}
	for i := range p {
		if p[i] != s[i] {
			return false
		}
	}
	return true
}
