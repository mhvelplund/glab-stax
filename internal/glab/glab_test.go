package glab

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

func TestParseRemoteURL(t *testing.T) {
	cases := map[string]Project{
		"git@gitlab.com:group/repo.git":                {"gitlab.com", "group/repo"},
		"ssh://git@gitlab.example:2222/a/b/c.git":      {"gitlab.example", "a/b/c"},
		"https://gitlab.com/group/sub/repo":            {"gitlab.com", "group/sub/repo"},
		"https://oauth2:tok@gitlab.com/group/repo.git": {"gitlab.com", "group/repo"},
		"work-gl:team/repo.git":                        {"work-gl", "team/repo"},
	}
	for in, want := range cases {
		got, err := ParseRemoteURL(in)
		if err != nil || got != want {
			t.Errorf("ParseRemoteURL(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"/local/path", "https://gitlab.com/onlyone", ""} {
		if _, err := ParseRemoteURL(bad); err == nil {
			t.Errorf("ParseRemoteURL(%q) should fail", bad)
		}
	}
}

func TestBlockRoundTrip(t *testing.T) {
	info := StackInfo{Trunk: "main", MRs: []StackItem{{1, "a"}, {2, "b-->x"}, {3, "c"}}}
	block := RenderBlock(info, 2)
	if strings.Count(block, "-->") != 2 {
		t.Fatalf("branch name leaked a comment terminator:\n%s", block)
	}
	if !strings.Contains(block, "- **!2** ← this MR\n") || !strings.Contains(block, "- !3\n- **!2**") {
		t.Fatalf("unexpected render:\n%s", block)
	}
	desc := SetBlock("User text.", block)
	got, ok := ParseBlock(desc)
	if !ok || got.Trunk != "main" || !reflect.DeepEqual(got.MRs, info.MRs) || got.Version != 1 {
		t.Fatalf("ParseBlock = %+v, %v", got, ok)
	}
	// Replacing keeps the user's text and leaves exactly one block.
	again := SetBlock(desc, RenderBlock(info, 3))
	if strings.Count(again, Marker) != 1 || !strings.HasPrefix(again, "User text.\n\n") {
		t.Fatalf("SetBlock did not replace:\n%s", again)
	}
	if StripBlock(again) != "User text." {
		t.Fatalf("StripBlock = %q", StripBlock(again))
	}
	if SetBlock("", block) != block {
		t.Fatal("SetBlock on empty description should be just the block")
	}
	if _, ok := ParseBlock("no block here"); ok {
		t.Fatal("ParseBlock found a block in plain text")
	}
}

func TestStripDraft(t *testing.T) {
	for in, want := range map[string]string{
		"Draft: Add x":        "Add x",
		"draft:[Draft] Add x": "Add x",
		"(Draft) Add x":       "Add x",
		"Add draft: mode":     "Add draft: mode",
	} {
		if got := StripDraft(in); got != want {
			t.Errorf("StripDraft(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPIArgsAndErrors(t *testing.T) {
	var got []string
	c := &Client{Project: Project{"gl.example", "g/r"}, Run: func(args ...string) ([]byte, error) {
		got = args
		return []byte(`[{"iid":4,"state":"merged"},{"iid":5,"state":"opened"}]`), nil
	}}
	mr, err := c.FindMRForBranch("feat")
	if err != nil || mr.IID != 5 {
		t.Fatalf("FindMRForBranch = %+v, %v", mr, err)
	}
	want := []string{"api", "--hostname", "gl.example", "--placeholder", "project=g/r", "-X", "GET", "projects/:project/merge_requests",
		"-f", "order_by=created_at", "-f", "sort=desc", "-f", "source_branch=feat", "-f", "state=all"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args =\n%v\nwant\n%v", got, want)
	}

	c.Run = func(...string) ([]byte, error) { return nil, errors.New("401 Unauthorized") }
	if _, err := c.GetMR(1); exitcode.Of(err) != exitcode.GitLabFailure {
		t.Fatalf("GetMR error exit = %d", exitcode.Of(err))
	}
}
