package exitcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestOf(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, OK},
		{errors.New("plain"), Generic},
		{New(RebaseConflict, "conflict in %s", "a.go"), RebaseConflict},
		{fmt.Errorf("outer: %w", New(Locked, "locked")), Locked},
		{Wrap(GitLabFailure, errors.New("api")), GitLabFailure},
	}
	for _, c := range cases {
		if got := Of(c.err); got != c.want {
			t.Errorf("Of(%v) = %d, want %d", c.err, got, c.want)
		}
	}
	if Wrap(Generic, nil) != nil {
		t.Error("Wrap(nil) should be nil")
	}
}
