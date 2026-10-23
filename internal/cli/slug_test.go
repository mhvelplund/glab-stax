package cli

import (
	"testing"
	"time"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add login: v2!": "add_login_v2",
		"fix--retry":     "fix-retry",
		"  ---  ":        "",
		"A very long commit message that goes on": "a_very_long_commit_message",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	now := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	if got := dateSlug(now, "!!"); got != "03-04" {
		t.Errorf("dateSlug empty = %q", got)
	}
}
