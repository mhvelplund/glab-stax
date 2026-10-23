// Package glabtest provides an in-memory GitLab that answers `glab api` calls.
package glabtest

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mhvelplund/glab-stax/internal/glab"
)

// Call is one recorded glab invocation.
type Call struct {
	Method, Endpoint string
	Fields           map[string]string
}

// GitLab is a fake project's merge requests.
type GitLab struct {
	mu      sync.Mutex
	MRs     map[int]*glab.MR
	nextIID int
	Calls   []Call
	// Fail makes the next call matching "METHOD endpoint-suffix" fail.
	Fail map[string]error
	// OnMerge runs when an MR is merged, e.g. to update the git remote.
	OnMerge func(mr *glab.MR, squash bool) error
	// Project is returned by GET projects/:project.
	Project glab.ProjectInfo
	// PendingPolls makes a merged MR report "opened" with auto-merge set for
	// this many GETs before it shows as merged, like a running pipeline.
	PendingPolls int
	pending      map[int]int
}

// New returns an empty fake.
func New() *GitLab {
	return &GitLab{MRs: map[int]*glab.MR{}, nextIID: 1, Fail: map[string]error{}}
}

// Client returns a glab.Client backed by the fake.
func (g *GitLab) Client() *glab.Client {
	return &glab.Client{Run: g.Run, Project: glab.Project{Host: "gitlab.example", Path: "group/repo"}}
}

// Add inserts an MR directly and returns it.
func (g *GitLab) Add(mr glab.MR) *glab.MR {
	g.mu.Lock()
	defer g.mu.Unlock()
	if mr.IID == 0 {
		mr.IID = g.nextIID
	}
	if mr.IID >= g.nextIID {
		g.nextIID = mr.IID + 1
	}
	if mr.State == "" {
		mr.State = "opened"
	}
	mr.WebURL = fmt.Sprintf("https://gitlab.example/group/repo/-/merge_requests/%d", mr.IID)
	mr.Draft = isDraft(mr.Title)
	m := mr
	g.MRs[mr.IID] = &m
	return &m
}

// CallsTo returns recorded calls whose "METHOD endpoint" contains substr.
func (g *GitLab) CallsTo(substr string) []Call {
	var out []Call
	for _, c := range g.Calls {
		if strings.Contains(c.Method+" "+c.Endpoint, substr) {
			out = append(out, c)
		}
	}
	return out
}

// Run implements glab.Runner for `glab api` invocations.
func (g *GitLab) Run(args ...string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, err := parse(args)
	if err != nil {
		return nil, err
	}
	g.Calls = append(g.Calls, c)
	for key, ferr := range g.Fail {
		if strings.HasPrefix(c.Method+" "+c.Endpoint, key) || strings.HasSuffix(c.Method+" "+c.Endpoint, key) {
			delete(g.Fail, key)
			return nil, ferr
		}
	}
	if c.Endpoint == "projects/:project" {
		return json.Marshal(g.Project)
	}
	const base = "projects/:project/merge_requests"
	rest, ok := strings.CutPrefix(c.Endpoint, base)
	if !ok {
		return nil, fmt.Errorf("fake: unsupported endpoint %s", c.Endpoint)
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case rest == "" && c.Method == "GET":
		return json.Marshal(g.list(c.Fields))
	case rest == "" && c.Method == "POST":
		return json.Marshal(g.create(c.Fields))
	}
	iid, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("fake: bad iid in %s", c.Endpoint)
	}
	mr := g.MRs[iid]
	if mr == nil {
		return nil, fmt.Errorf("404 Not Found")
	}
	switch {
	case len(parts) == 1 && c.Method == "GET":
		if n := g.pending[iid]; n > 0 {
			g.pending[iid] = n - 1
			view := *mr
			view.State, view.AutoMerge = "opened", true
			return json.Marshal(&view)
		}
	case len(parts) == 1 && c.Method == "PUT":
		g.update(mr, c.Fields)
	case len(parts) == 2 && parts[1] == "cancel_merge_when_pipeline_succeeds":
		mr.AutoMerge = false
	case len(parts) == 2 && parts[1] == "merge" && c.Method == "PUT":
		if !mr.Open() || mr.Draft {
			return nil, fmt.Errorf("405 Method Not Allowed")
		}
		if c.Fields["merge_when_pipeline_succeeds"] == "true" || c.Fields["auto_merge"] == "true" {
			mr.AutoMerge = true
		}
		if sha := c.Fields["sha"]; sha != "" && mr.SHA != "" && sha != mr.SHA {
			return nil, fmt.Errorf("409 SHA does not match HEAD of source branch")
		}
		if g.OnMerge != nil {
			if err := g.OnMerge(mr, c.Fields["squash"] == "true"); err != nil {
				return nil, err
			}
		}
		mr.State = "merged"
		if g.PendingPolls > 0 {
			if g.pending == nil {
				g.pending = map[int]int{}
			}
			g.pending[iid] = g.PendingPolls
			view := *mr
			view.State, view.AutoMerge = "opened", true
			return json.Marshal(&view)
		}
	default:
		return nil, fmt.Errorf("fake: unsupported %s %s", c.Method, c.Endpoint)
	}
	return json.Marshal(mr)
}

func (g *GitLab) list(f map[string]string) []*glab.MR {
	var ids []int
	for id := range g.MRs {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids))) // newest first
	out := []*glab.MR{}
	for _, id := range ids {
		mr := g.MRs[id]
		if v := f["source_branch"]; v != "" && mr.SourceBranch != v {
			continue
		}
		if v := f["state"]; v != "" && v != "all" && mr.State != v {
			continue
		}
		if v := f["search"]; v != "" && !strings.Contains(mr.Description, v) {
			continue
		}
		out = append(out, mr)
	}
	return out
}

func (g *GitLab) create(f map[string]string) *glab.MR {
	mr := glab.MR{
		IID: g.nextIID, Title: f["title"], Description: f["description"], State: "opened",
		SourceBranch: f["source_branch"], TargetBranch: f["target_branch"],
	}
	g.nextIID++
	mr.WebURL = fmt.Sprintf("https://gitlab.example/group/repo/-/merge_requests/%d", mr.IID)
	mr.Draft = isDraft(mr.Title)
	g.MRs[mr.IID] = &mr
	return &mr
}

func (g *GitLab) update(mr *glab.MR, f map[string]string) {
	if v, ok := f["title"]; ok {
		mr.Title, mr.Draft = v, isDraft(v)
	}
	if v, ok := f["description"]; ok {
		mr.Description = v
	}
	if v, ok := f["target_branch"]; ok {
		mr.TargetBranch = v
	}
	if v, ok := f["state_event"]; ok && v == "close" {
		mr.State = "closed"
	}
}

func isDraft(title string) bool { return glab.StripDraft(title) != strings.TrimSpace(title) }

func parse(args []string) (Call, error) {
	c := Call{Method: "GET", Fields: map[string]string{}}
	if len(args) == 0 || args[0] != "api" {
		return c, fmt.Errorf("fake: only `glab api` is supported, got %v", args)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--hostname", "--placeholder":
			i++
		case "-X":
			i++
			c.Method = args[i]
		case "-f", "-F":
			i++
			k, v, _ := strings.Cut(args[i], "=")
			c.Fields[k] = v
		default:
			c.Endpoint = args[i]
		}
	}
	return c, nil
}
