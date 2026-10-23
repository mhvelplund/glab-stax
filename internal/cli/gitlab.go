package cli

import (
	"fmt"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
	"github.com/mhvelplund/glab-stax/internal/glab"
)

// gitlab returns a client for the project behind remote.
func (a *App) gitlab(remote string) (*glab.Client, error) {
	if a.NewGitLab != nil {
		return a.NewGitLab(remote)
	}
	raw, err := a.repo().Run("remote", "get-url", remote)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.InvalidArgs, fmt.Errorf("remote %q: %w", remote, err))
	}
	p, err := glab.ParseRemoteURL(raw)
	if err != nil {
		return nil, exitcode.Wrap(exitcode.GitLabFailure, err)
	}
	p.Host = glab.SSHHostname(p.Host)
	return &glab.Client{Run: glab.ExecRunner, Project: p}, nil
}

func pickRemote(flag, stackRemote string) string {
	if flag != "" {
		return flag
	}
	if stackRemote != "" {
		return stackRemote
	}
	return "origin"
}
