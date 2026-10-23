package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// withPager sends what fn writes through $GIT_PAGER, $PAGER or `less -R`
// when stdout is a terminal.
func (a *App) withPager(fn func(w io.Writer) error) error {
	if !a.OutIsTTY {
		return fn(a.Out)
	}
	pager := firstNonEmpty(os.Getenv("GIT_PAGER"), os.Getenv("PAGER"), "less -R")
	if pager == "cat" {
		return fn(a.Out)
	}
	cmd := exec.Command("sh", "-c", pager)
	cmd.Stdout, cmd.Stderr = a.Out, a.Err
	cmd.Env = append(os.Environ(), "LESS=FRX")
	w, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fn(a.Out)
	}
	ferr := fn(w)
	w.Close()
	if err := cmd.Wait(); err != nil && ferr == nil {
		return nil // quitting the pager early is not an error
	}
	return ferr
}

// hyperlinks reports whether to emit OSC 8 links. GLAB_STAX_HYPERLINKS=1/0 forces it.
func (a *App) hyperlinks() bool {
	switch os.Getenv("GLAB_STAX_HYPERLINKS") {
	case "1":
		return true
	case "0":
		return false
	}
	return a.OutIsTTY && os.Getenv("TERM") != "dumb"
}

func osc8(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

func relTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	case d < 365*24*time.Hour:
		return plural(int(d.Hours()/24/30), "month") + " ago"
	default:
		return plural(int(d.Hours()/24/365), "year") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
