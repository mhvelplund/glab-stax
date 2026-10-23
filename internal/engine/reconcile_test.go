package engine

import (
	"testing"

	"github.com/mhvelplund/glab-stax/internal/glab"
	"github.com/mhvelplund/glab-stax/internal/stack"
)

func mkStack(spec ...string) *stack.Stack {
	s := &stack.Stack{Trunk: "main"}
	for i, n := range spec {
		b := &stack.Branch{Name: n}
		if n[0] != '_' {
			b.MR = &stack.MR{IID: i + 1}
		} else {
			b.Name = n[1:]
		}
		s.Branches = append(s.Branches, b)
	}
	return s
}

func info(branches ...string) *glab.StackInfo {
	in := &glab.StackInfo{Trunk: "main"}
	for i, b := range branches {
		in.MRs = append(in.MRs, glab.StackItem{IID: i + 1, Branch: b})
	}
	return in
}

func TestCompare(t *testing.T) {
	cases := []struct {
		name   string
		local  *stack.Stack
		remote *glab.StackInfo
		want   Relation
	}{
		{"none", mkStack("a"), nil, NoRemoteStack},
		{"same", mkStack("a", "b"), info("a", "b"), InSync},
		{"same plus unsubmitted top", mkStack("a", "b", "_c"), info("a", "b"), InSync},
		{"local ahead", mkStack("a", "b", "c"), info("a", "b"), LocalAhead},
		{"remote ahead", mkStack("a"), info("a", "b"), RemoteAhead},
		{"remote ahead but local has unsubmitted", mkStack("a", "_x"), info("a", "b"), Diverged},
		{"reordered", mkStack("b", "a"), info("a", "b"), Diverged},
		{"unsubmitted in the middle", mkStack("a", "_x", "b"), info("a", "b"), InSync},
		{"remote ahead, unsubmitted in the middle", mkStack("a", "_x", "b"), info("a", "b", "c"), RemoteAhead},
	}
	for _, c := range cases {
		if got := Compare(c.local, c.remote); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
