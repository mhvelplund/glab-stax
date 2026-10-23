package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAliasInstallAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var calls [][]string
	var errb bytes.Buffer
	app := &App{Dir: home, Out: &bytes.Buffer{}, Err: &errb, In: strings.NewReader(""), Now: time.Now,
		Glab: func(args ...string) ([]byte, error) { calls = append(calls, args); return nil, nil }}

	if err := Execute(app, []string{"alias"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".local", "bin", "gs")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `exec glab-stax "$@"`) {
		t.Fatalf("wrapper = %q, %v", data, err)
	}
	if fi, _ := os.Stat(path); fi.Mode()&0o111 == 0 {
		t.Fatal("wrapper not executable")
	}
	if len(calls) != 1 || strings.Join(calls[0], " ") != `alias set stax glab-stax "$@" --shell` {
		t.Fatalf("glab calls = %v", calls)
	}

	// A file we did not write is left alone.
	foreign := filepath.Join(home, ".local", "bin", "mine")
	if err := os.WriteFile(foreign, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Execute(app, []string{"alias", "mine"}); err == nil {
		t.Fatal("overwrote a foreign file")
	}
	if err := Execute(app, []string{"alias", "--remove", "mine"}); err == nil {
		t.Fatal("removed a foreign file")
	}

	if err := Execute(app, []string{"alias", "--remove"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("wrapper not removed")
	}
	if last := calls[len(calls)-1]; strings.Join(last, " ") != "alias delete stax" {
		t.Fatalf("last glab call = %v", last)
	}
}

func TestFeedback(t *testing.T) {
	var opened string
	var out bytes.Buffer
	app := &App{Dir: t.TempDir(), Out: &out, Err: &bytes.Buffer{}, In: strings.NewReader(""), Now: time.Now,
		OpenURL: func(u string) error { opened = u; return nil }}
	if err := Execute(app, []string{"feedback", "merge", "is", "slow"}); err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/mhvelplund/glab-stax/issues/new?title=merge+is+slow"
	if opened != want || strings.TrimSpace(out.String()) != want {
		t.Fatalf("opened %q, printed %q", opened, out.String())
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.2.0", "v1.1.9", true},
		{"v1.2.0", "1.2.0", false},
		{"v0.10.0", "v0.9.1", true},
		{"v1.0.0", "v1.0.0-rc1", false},
		{"garbage", "v1.0.0", false},
		{"", "v1.0.0", false},
	}
	for _, c := range cases {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
