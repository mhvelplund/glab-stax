package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
)

func (h *harness) remoteSHA(branch string) string {
	return h.repo.Git("ls-remote", "origin", "refs/heads/"+branch)
}

func TestPush(t *testing.T) {
	h := threeLayers(t)
	h.must("push")
	for _, b := range []string{"a", "b", "c"} {
		if !strings.HasPrefix(h.remoteSHA(b), h.sha(b)) {
			t.Fatalf("%s not pushed", b)
		}
	}
	// Rewritten history is force-pushed with a lease.
	h.repo.Git("checkout", "--quiet", "a")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended")
	h.must("rebase", "--no-trunk")
	h.must("push")
	if !strings.HasPrefix(h.remoteSHA("c"), h.sha("c")) {
		t.Fatal("rebased c not pushed")
	}
	// Someone else pushed to b: the lease must refuse to clobber it.
	h.repo.PushFromElsewhere("b", "theirs.txt", "x\n", "their commit")
	h.repo.Git("commit", "--quiet", "--amend", "-m", "a1 amended again")
	h.must("rebase", "--no-trunk")
	if r := h.run("push"); r.e == nil {
		t.Fatal("push overwrote someone else's commit")
	}
}

func TestSubmitCreatesStackOfMRs(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")

	if len(h.gl.MRs) != 3 {
		t.Fatalf("created %d MRs", len(h.gl.MRs))
	}
	want := map[string]struct{ target, title string }{
		"a": {"main", "Draft: a1"},
		"b": {"a", "Draft: b"}, // two commits: humanized branch name
		"c": {"b", "Draft: c1"},
	}
	for _, mr := range h.gl.MRs {
		w := want[mr.SourceBranch]
		if mr.TargetBranch != w.target || mr.Title != w.title || !mr.Draft {
			t.Errorf("MR for %s = target %s, title %q, draft %v", mr.SourceBranch, mr.TargetBranch, mr.Title, mr.Draft)
		}
		info, ok := glab.ParseBlock(mr.Description)
		if !ok || len(info.MRs) != 3 || info.Trunk != "main" || info.MRs[0].Branch != "a" {
			t.Errorf("MR !%d block = %+v, %v", mr.IID, info, ok)
		}
		if !strings.Contains(mr.Description, "**!"+strconv.Itoa(mr.IID)+"** ← this MR") {
			t.Errorf("MR !%d does not highlight itself:\n%s", mr.IID, mr.Description)
		}
	}
	s := h.file().Stacks[0]
	for _, b := range s.Branches {
		if b.MR == nil || b.MR.URL == "" {
			t.Fatalf("%s has no MR recorded", b.Name)
		}
	}

	// A second submit changes nothing on GitLab.
	before := len(h.gl.Calls)
	h.must("submit", "--auto")
	for _, c := range h.gl.Calls[before:] {
		if c.Method != "GET" {
			t.Errorf("idempotent submit made a write: %s %s", c.Method, c.Endpoint)
		}
	}
}

func TestSubmitOpenAndRepairs(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	mrB := h.gl.MRs[2]
	mrB.TargetBranch = "main"
	mrB.AutoMerge = true

	h.must("submit", "--open")
	if mrB.TargetBranch != "a" || mrB.AutoMerge {
		t.Fatalf("MR b not repaired: target %s, auto-merge %v", mrB.TargetBranch, mrB.AutoMerge)
	}
	for _, mr := range h.gl.MRs {
		if mr.Draft {
			t.Errorf("!%d still draft", mr.IID)
		}
	}
}

func TestSubmitAfterBottomMerged(t *testing.T) {
	h := threeLayers(t)
	h.must("submit", "--auto")
	h.gl.MRs[1].State = "merged"

	h.must("submit", "--auto")
	if h.gl.MRs[2].TargetBranch != "main" {
		t.Fatalf("b's MR targets %s, want main", h.gl.MRs[2].TargetBranch)
	}
	if !h.file().Stacks[0].Branches[0].Merged {
		t.Fatal("a not marked merged")
	}
	if len(h.gl.MRs) != 3 {
		t.Fatal("a new MR was created for a merged branch")
	}
}

func TestSubmitStartsFreshStackWhenAllMerged(t *testing.T) {
	h := newHarness(t)
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")
	h.must("submit", "--auto")
	h.gl.MRs[1].State = "merged"
	writeFile(t, h.repo.Dir+"/n.txt", "n\n")
	h.must("add", "-A", "-m", "next thing")

	h.must("submit", "--auto")
	s := h.file().Stacks[0]
	if len(s.Branches) != 1 || s.Branches[0].Name != "03-24-next_thing" {
		t.Fatalf("merged layers not pruned: %v", names(s))
	}
	mr := h.gl.MRs[2]
	info, _ := glab.ParseBlock(mr.Description)
	if mr.TargetBranch != "main" || len(info.MRs) != 1 {
		t.Fatalf("new MR target %s, block %+v", mr.TargetBranch, info)
	}
}

func TestSubmitUsesTemplateAndPrompts(t *testing.T) {
	h := newHarness(t)
	tmpl := filepath.Join(h.repo.Dir, ".gitlab", "merge_request_templates")
	if err := os.MkdirAll(tmpl, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmpl, "Default.md"), "## Checklist\n")
	h.repo.Git("add", ".gitlab")
	h.repo.Git("commit", "--quiet", "-m", "template")
	h.must("init", "a")
	h.repo.Commit("a.txt", "a\n", "a1")

	r := h.runIn(h.repo.Dir, "My title\n", true, "submit")
	if r.e != nil {
		t.Fatal(r.e, r.err)
	}
	mr := h.gl.MRs[1]
	if mr.Title != "My title" || mr.Draft || !strings.HasPrefix(mr.Description, "## Checklist\n") {
		t.Fatalf("MR = %+v", mr)
	}
}

func TestSubmitPartialFailureKeepsProgress(t *testing.T) {
	h := threeLayers(t)
	creates := 0
	h.runner = func(args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "-X POST projects/:project/merge_requests") {
			if creates++; creates == 2 {
				return nil, errors.New("500 Internal Server Error")
			}
		}
		return h.gl.Run(args...)
	}
	r := h.run("submit", "--auto")
	if exitcode.Of(r.e) != exitcode.GitLabFailure {
		t.Fatalf("exit %d: %v", exitcode.Of(r.e), r.e)
	}
	s := h.file().Stacks[0]
	if s.Branches[0].MR == nil || s.Branches[1].MR != nil || s.Branches[2].MR == nil {
		t.Fatalf("progress not saved: %+v %+v %+v", s.Branches[0].MR, s.Branches[1].MR, s.Branches[2].MR)
	}
}
