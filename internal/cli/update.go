package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/mhvelplund/glab-stax/internal/glab"
)

const (
	repoURL       = "https://github.com/mhvelplund/glab-stax"
	releasesAPI   = "https://api.github.com/repos/mhvelplund/glab-stax/releases/latest"
	checkInterval = 24 * time.Hour
)

var glabExec = glab.ExecRunner

func issueURL(title string) string {
	u := repoURL + "/issues/new"
	if title != "" {
		u += "?title=" + url.QueryEscape(title)
	}
	return u
}

func (a *App) openURL(u string) error {
	if a.OpenURL != nil {
		return a.OpenURL(u)
	}
	opener := "xdg-open"
	if _, err := exec.LookPath("open"); err == nil {
		opener = "open"
	}
	return exec.Command(opener, u).Start()
}

// version returns the build version: -ldflags, else the module version from go install.
func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return Version
}

type updateCache struct {
	CheckedAt time.Time `json:"checkedAt"`
	Latest    string    `json:"latest"`
}

// updateNotifier checks for a newer release at most once a day in the
// background and returns a function that prints a notice, if any.
func (a *App) updateNotifier() func() {
	cur := version()
	if cur == "dev" || os.Getenv("GLAB_STAX_NO_UPDATE_NOTIFIER") != "" || !a.ErrIsTTY {
		return func() {}
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return func() {}
	}
	path := filepath.Join(dir, "glab-stax", "update.json")
	var cache updateCache
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &cache)
	}
	done := make(chan struct{})
	if time.Since(cache.CheckedAt) > checkInterval {
		go func() {
			defer close(done)
			if latest, err := fetchLatest(); err == nil {
				cache = updateCache{CheckedAt: time.Now(), Latest: latest}
				if data, err := json.Marshal(cache); err == nil {
					_ = os.MkdirAll(filepath.Dir(path), 0o755)
					_ = os.WriteFile(path, data, 0o644)
				}
			}
		}()
	} else {
		close(done)
	}
	return func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			return
		}
		if newer(cache.Latest, cur) {
			fmt.Fprintf(a.Err, "\nA new release of glab-stax is available: %s → %s\n%s/releases/latest\n", cur, cache.Latest, repoURL)
		}
	}
}

func fetchLatest() (string, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(releasesAPI)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("releases: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// newer reports whether semver tag a is greater than b (leading "v" optional).
func newer(a, b string) bool {
	pa, pb := semver(a), semver(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semver(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
