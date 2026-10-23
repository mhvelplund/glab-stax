package cli

import (
	"bytes"
	"testing"

	"glab-stax/internal/exitcode"
)

func TestBadFlagIsInvalidArgs(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{"--no-such-flag"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	if got := exitcode.Of(root.Execute()); got != exitcode.InvalidArgs {
		t.Fatalf("exit code = %d, want %d", got, exitcode.InvalidArgs)
	}
}
