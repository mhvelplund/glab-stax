package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

// Store reads and writes the metadata file in the git common dir.
type Store struct {
	Dir string // git common dir
}

func (st Store) path() string     { return filepath.Join(st.Dir, "glab-stax") }
func (st Store) lockPath() string { return filepath.Join(st.Dir, "glab-stax.lock") }

// Load reads the metadata file. A missing file is an empty File.
func (st Store) Load() (*File, error) {
	data, err := os.ReadFile(st.path())
	if errors.Is(err, os.ErrNotExist) {
		return &File{Version: FormatVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", st.path(), err)
	}
	if f.Version > FormatVersion {
		return nil, fmt.Errorf("%s was written by a newer glab-stax (format %d)", st.path(), f.Version)
	}
	return &f, nil
}

// Save writes f atomically.
func (st Store) Save(f *File) error {
	f.Version = FormatVersion
	return st.writeJSON(st.path(), f)
}

// writeJSON writes v to path atomically, through a temporary file.
func (st Store) writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(st.Dir, "glab-stax.tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Lock serializes mutations across all worktrees of the clone. A lock left by
// a dead process is reclaimed.
func (st Store) Lock() (unlock func(), err error) {
	p := st.lockPath()
	for attempt := 0; attempt < 2; attempt++ {
		err = writeLock(p, os.Getpid())
		if err == nil {
			return func() { os.Remove(p) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		pid := readLockPID(p)
		if pid > 0 && processAlive(pid) {
			return nil, exitcode.New(exitcode.Locked, "stack is locked by another glab-stax process (pid %d); if it is gone, remove %s", pid, p)
		}
		if pid <= 0 && lockIsFresh(p) {
			// Its owner created it a moment ago and has not written its PID yet.
			return nil, exitcode.New(exitcode.Locked, "stack is locked by another glab-stax process; if it is gone, remove %s", p)
		}
		os.Remove(p)
	}
	return nil, exitcode.New(exitcode.Locked, "could not acquire %s", p)
}

// lockIsFresh reports whether the lock file was written in the last few seconds.
func lockIsFresh(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && time.Since(fi.ModTime()) < 10*time.Second
}

func writeLock(path string, pid int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString(strconv.Itoa(pid))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func readLockPID(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return pid
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
