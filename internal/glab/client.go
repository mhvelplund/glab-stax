// Package glab talks to GitLab through the glab CLI.
package glab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

// Runner executes glab with args and returns stdout.
type Runner func(args ...string) ([]byte, error)

// ExecRunner runs the glab binary.
func ExecRunner(args ...string) ([]byte, error) {
	cmd := exec.Command("glab", args...)
	cmd.Env = append(os.Environ(), "GLAB_CHECK_UPDATE=false", "NO_COLOR=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("glab %s: %s (%w)", strings.Join(args[:min(len(args), 8)], " "), msg, err)
	}
	return stdout.Bytes(), nil
}

// Client makes GitLab API calls for one project.
type Client struct {
	Run     Runner
	Project Project
}

// MR is the subset of a GitLab merge request that glab-stax uses.
type MR struct {
	IID          int    `json:"iid"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	State        string `json:"state"` // opened, closed, merged, locked
	Draft        bool   `json:"draft"`
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	WebURL       string `json:"web_url"`
	SHA          string `json:"sha"`
	AutoMerge    bool   `json:"merge_when_pipeline_succeeds"`
	// RemoveSource is the MR's "delete source branch" option.
	RemoveSource bool `json:"force_remove_source_branch"`
	// MergeStatus is GitLab's detailed_merge_status (mergeable, checking, ...).
	MergeStatus string `json:"detailed_merge_status"`
	MergeError  string `json:"merge_error"`
	// Squash is the MR's own "squash commits" setting, which GitLab applies
	// when merging unless the project forces squashing on or off.
	Squash bool `json:"squash"`
	// SourceProjectID differs from TargetProjectID for an MR from a fork.
	SourceProjectID int `json:"source_project_id"`
	TargetProjectID int `json:"target_project_id"`
}

// Checking reports whether GitLab is still computing mergeability.
func (m *MR) Checking() bool {
	switch m.MergeStatus {
	case "checking", "unchecked", "preparing", "approvals_syncing":
		return true
	}
	return false
}

// Merged reports whether the MR has been merged.
func (m *MR) Merged() bool { return m.State == "merged" }

// Open reports whether the MR is open.
func (m *MR) Open() bool { return m.State == "opened" }

// fromFork reports whether the MR's source branch lives in another project.
func (m *MR) fromFork() bool { return m.SourceProjectID != m.TargetProjectID }

// project is the API path of the project, URL-encoded as GitLab expects.
func (c *Client) project() string { return "projects/" + url.PathEscape(c.Project.Path) }

func (c *Client) mrs() string { return c.project() + "/merge_requests" }

func (c *Client) mr(iid int) string { return c.mrs() + "/" + strconv.Itoa(iid) }

// api calls `glab api`. String fields are sent with -f, everything else with -F.
func (c *Client) api(method, endpoint string, fields map[string]any, out any) error {
	args := []string{"api", "--hostname", c.Project.Host, "-X", method, endpoint}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := fields[k].(type) {
		case string:
			args = append(args, "-f", k+"="+v)
		default:
			args = append(args, "-F", fmt.Sprintf("%s=%v", k, v))
		}
	}
	data, err := c.Run(args...)
	if err != nil {
		return exitcode.Wrap(exitcode.GitLabFailure, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return exitcode.Wrap(exitcode.GitLabFailure, fmt.Errorf("parse glab api %s response: %w", endpoint, err))
	}
	return nil
}

// GetMR fetches one MR by IID.
func (c *Client) GetMR(iid int) (*MR, error) {
	var mr MR
	if err := c.api("GET", c.mr(iid), nil, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

// FindMRForBranch returns the open MR for branch, else the most recently
// merged one, else nil. MRs from forks are ignored: their source branch is
// someone else's branch that only shares the name.
func (c *Client) FindMRForBranch(branch string) (*MR, error) {
	var list []*MR
	if err := c.api("GET", c.mrs(), map[string]any{"source_branch": branch, "state": "all", "order_by": "created_at", "sort": "desc"}, &list); err != nil {
		return nil, err
	}
	for _, m := range list {
		if m.Open() && !m.fromFork() {
			return m, nil
		}
	}
	for _, m := range list {
		if m.Merged() && !m.fromFork() {
			return m, nil
		}
	}
	return nil, nil
}

// SearchMRs returns open MRs whose description contains text.
func (c *Client) SearchMRs(text string) ([]*MR, error) {
	var list []*MR
	err := c.api("GET", c.mrs(), map[string]any{"search": text, "in": "description", "state": "opened", "per_page": 100}, &list)
	return list, err
}

// ListMRsTargeting returns open MRs, not from forks, whose target branch is branch.
func (c *Client) ListMRsTargeting(branch string) ([]*MR, error) {
	var list []*MR
	if err := c.api("GET", c.mrs(), map[string]any{"target_branch": branch, "state": "opened", "per_page": 100}, &list); err != nil {
		return nil, err
	}
	out := list[:0]
	for _, m := range list {
		if !m.fromFork() {
			out = append(out, m)
		}
	}
	return out, nil
}

// NewMR describes a merge request to create.
type NewMR struct {
	Source, Target, Title, Description string
	Draft                              bool
}

// CreateMR opens a merge request.
func (c *Client) CreateMR(n NewMR) (*MR, error) {
	title := n.Title
	if n.Draft {
		title = "Draft: " + StripDraft(title)
	}
	var mr MR
	err := c.api("POST", c.mrs(), map[string]any{
		"source_branch": n.Source, "target_branch": n.Target,
		"title": title, "description": n.Description,
	}, &mr)
	if err != nil {
		return nil, err
	}
	return &mr, nil
}

// UpdateMR changes the given attributes (target_branch, title, description, ...).
func (c *Client) UpdateMR(iid int, fields map[string]any) (*MR, error) {
	var mr MR
	if err := c.api("PUT", c.mr(iid), fields, &mr); err != nil {
		return nil, err
	}
	return &mr, nil
}

// MarkReady removes the draft status from an MR.
func (c *Client) MarkReady(mr *MR) (*MR, error) {
	return c.UpdateMR(mr.IID, map[string]any{"title": StripDraft(mr.Title)})
}

// CancelAutoMerge turns off "merge when pipeline succeeds".
func (c *Client) CancelAutoMerge(iid int) error {
	return c.api("POST", c.mr(iid)+"/cancel_merge_when_pipeline_succeeds", nil, nil)
}

// Merge asks GitLab to merge the MR at sha. It is requested as auto-merge, so
// GitLab merges at once when nothing is pending, waits for the pipeline, or
// adds the MR to a merge train. The source branch is kept; callers delete it
// once dependent MRs are retargeted.
func (c *Client) Merge(iid int, squash bool, sha string) (*MR, error) {
	var mr MR
	err := c.api("PUT", c.mr(iid)+"/merge", map[string]any{
		"squash": squash, "sha": sha, "should_remove_source_branch": false,
		"auto_merge": true, "merge_when_pipeline_succeeds": true,
	}, &mr)
	if err != nil {
		return nil, err
	}
	return &mr, nil
}

// BranchInfo is the subset of a repository branch that glab-stax uses.
type BranchInfo struct {
	Protected bool `json:"protected"`
	Default   bool `json:"default"`
}

// GetBranch fetches a branch of the project.
func (c *Client) GetBranch(name string) (*BranchInfo, error) {
	var b BranchInfo
	if err := c.api("GET", c.project()+"/repository/branches/"+url.PathEscape(name), nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ProjectInfo is the subset of project settings glab-stax uses.
type ProjectInfo struct {
	SquashOption string `json:"squash_option"` // never, always, default_on, default_off
}

// GetProject fetches project settings.
func (c *Client) GetProject() (*ProjectInfo, error) {
	var p ProjectInfo
	if err := c.api("GET", c.project(), nil, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// StripDraft removes GitLab draft prefixes from a title.
func StripDraft(title string) string {
	for {
		t := strings.TrimSpace(title)
		lower := strings.ToLower(t)
		trimmed := false
		for _, p := range []string{"draft:", "[draft]", "(draft)"} {
			if strings.HasPrefix(lower, p) {
				title, trimmed = t[len(p):], true
				break
			}
		}
		if !trimmed {
			return strings.TrimSpace(title)
		}
	}
}
