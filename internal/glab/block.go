package glab

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	blockBegin = "<!-- glab-stax:begin "
	blockEnd   = "<!-- glab-stax:end -->"
	// Marker is searched for to find stacks on GitLab.
	Marker = "glab-stax:begin"
	// RepoURL is glab-stax's home page.
	RepoURL = "https://github.com/mhvelplund/glab-stax"
)

var blockRe = regexp.MustCompile(`(?s)\n*<!-- glab-stax:begin (\{.*?\}) -->.*?<!-- glab-stax:end -->\n*`)

// StackInfo is the machine-readable part of the block in each MR description.
type StackInfo struct {
	Version int         `json:"v"`
	Trunk   string      `json:"trunk"`
	MRs     []StackItem `json:"mrs"` // bottom first
}

// StackItem is one MR of a stack.
type StackItem struct {
	IID    int    `json:"iid"`
	Branch string `json:"branch"`
}

// RenderBlock renders the stack block for the MR with IID self.
func RenderBlock(info StackInfo, self int) string {
	info.Version = 1
	data, _ := json.Marshal(info) // escapes < and >, so the comment cannot be closed early
	var b strings.Builder
	b.WriteString(blockBegin + string(data) + " -->\n")
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "**Stack** (managed by [glab stax](%s))\n\n", RepoURL)
	for i := len(info.MRs) - 1; i >= 0; i-- {
		m := info.MRs[i]
		if m.IID == self {
			fmt.Fprintf(&b, "- **!%d** ← this MR\n", m.IID)
		} else {
			fmt.Fprintf(&b, "- !%d\n", m.IID)
		}
	}
	fmt.Fprintf(&b, "- `%s`\n", info.Trunk)
	b.WriteString(blockEnd)
	return b.String()
}

// ParseBlock extracts stack info from an MR description.
func ParseBlock(desc string) (*StackInfo, bool) {
	m := blockRe.FindStringSubmatch(desc)
	if m == nil {
		return nil, false
	}
	var info StackInfo
	if err := json.Unmarshal([]byte(m[1]), &info); err != nil {
		return nil, false
	}
	return &info, true
}

// SetBlock replaces the stack block in desc, or appends it.
func SetBlock(desc, block string) string {
	base := StripBlock(desc)
	if base == "" {
		return block
	}
	return base + "\n\n" + block
}

// StripBlock removes the stack block from desc. Text before and after a block
// in the middle stays in separate paragraphs.
func StripBlock(desc string) string {
	var b strings.Builder
	last := 0
	for _, m := range blockRe.FindAllStringIndex(desc, -1) {
		b.WriteString(desc[last:m[0]])
		if m[0] > 0 && m[1] < len(desc) {
			b.WriteString("\n\n")
		}
		last = m[1]
	}
	b.WriteString(desc[last:])
	return strings.TrimRight(b.String(), "\n ")
}
