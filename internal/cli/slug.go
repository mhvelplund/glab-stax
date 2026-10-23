package cli

import (
	"regexp"
	"strings"
	"time"
)

var (
	slugDisallowed = regexp.MustCompile(`[^a-z0-9-]+`)
	slugMultiSep   = regexp.MustCompile(`[-_]{2,}`)
)

// slugify turns a commit message into a branch-safe slug of at most ~30
// characters, as gh-stack does: disallowed runs become "_", typed hyphens survive.
func slugify(msg string) string {
	s := slugDisallowed.ReplaceAllString(strings.ToLower(msg), "_")
	s = slugMultiSep.ReplaceAllStringFunc(s, func(run string) string {
		if strings.ContainsRune(run, '-') {
			return "-"
		}
		return "_"
	})
	s = strings.Trim(s, "-_")
	if len(s) > 30 {
		s = s[:30]
		if i := strings.LastIndex(s, "_"); i > 0 {
			s = s[:i]
		}
		s = strings.Trim(s, "-_")
	}
	return s
}

// dateSlug returns MM-DD-<slug>, used to name a branch from a commit message.
func dateSlug(now time.Time, msg string) string {
	date := now.Format("01-02")
	if s := slugify(msg); s != "" {
		return date + "-" + s
	}
	return date
}
