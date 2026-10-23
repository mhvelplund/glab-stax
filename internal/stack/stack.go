// Package stack holds the stack model and its on-disk store.
package stack

import (
	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

// FormatVersion is the current metadata file format.
const FormatVersion = 1

// File is the whole metadata file: every stack in the clone.
type File struct {
	Version int      `json:"version"`
	Stacks  []*Stack `json:"stacks"`
}

// Stack is an ordered list of branches on top of a trunk; Branches[0] is the bottom.
type Stack struct {
	Trunk    string    `json:"trunk"`
	Remote   string    `json:"remote"`
	Branches []*Branch `json:"branches"`
}

// Branch is one layer of a stack.
type Branch struct {
	Name string `json:"name"`
	// Base is the parent's tip SHA at the last restack. Commits after Base are this layer's own.
	Base   string `json:"base,omitempty"`
	MR     *MR    `json:"mr,omitempty"`
	Merged bool   `json:"merged,omitempty"`
}

// MR identifies the merge request for a branch.
type MR struct {
	IID int    `json:"iid"`
	URL string `json:"url,omitempty"`
}

// Index returns the position of name in s, or -1.
func (s *Stack) Index(name string) int {
	for i, b := range s.Branches {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// Get returns the branch called name, or nil.
func (s *Stack) Get(name string) *Branch {
	if i := s.Index(name); i >= 0 {
		return s.Branches[i]
	}
	return nil
}

// Parent returns the name of the nearest unmerged branch below name, or the trunk.
func (s *Stack) Parent(name string) string {
	for i := s.Index(name) - 1; i >= 0; i-- {
		if !s.Branches[i].Merged {
			return s.Branches[i].Name
		}
	}
	return s.Trunk
}

// Active returns the unmerged branches, bottom first.
func (s *Stack) Active() []*Branch {
	var out []*Branch
	for _, b := range s.Branches {
		if !b.Merged {
			out = append(out, b)
		}
	}
	return out
}

// Top returns the topmost unmerged branch, or nil.
func (s *Stack) Top() *Branch {
	a := s.Active()
	if len(a) == 0 {
		return nil
	}
	return a[len(a)-1]
}

// Bottom returns the lowest unmerged branch, or nil.
func (s *Stack) Bottom() *Branch {
	a := s.Active()
	if len(a) == 0 {
		return nil
	}
	return a[0]
}

// Find returns the stack containing branch, or nil.
func (f *File) Find(branch string) *Stack {
	for _, s := range f.Stacks {
		if s.Index(branch) >= 0 {
			return s
		}
	}
	return nil
}

// Resolve finds the stack for branch, which may be a stack branch or a trunk
// used by exactly one stack.
func (f *File) Resolve(branch string) (*Stack, error) {
	if s := f.Find(branch); s != nil {
		return s, nil
	}
	var matches []*Stack
	for _, s := range f.Stacks {
		if s.Trunk == branch {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, exitcode.New(exitcode.NotInStack, "branch %q is not part of a stack", branch)
	case 1:
		return matches[0], nil
	default:
		return nil, exitcode.New(exitcode.Ambiguous, "%q is the trunk of %d stacks; check out a stack branch first", branch, len(matches))
	}
}

// Remove deletes s from the file.
func (f *File) Remove(s *Stack) {
	for i, x := range f.Stacks {
		if x == s {
			f.Stacks = append(f.Stacks[:i], f.Stacks[i+1:]...)
			return
		}
	}
}
