package stack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Journal names for paused multi-step operations.
const (
	RebaseJournal = "glab-stax-rebase-state"
	ModifyJournal = "glab-stax-modify-state"
)

// SaveJournal writes v as the named journal, atomically: a journal cut short
// would leave the paused operation impossible to continue or abort.
func (st Store) SaveJournal(name string, v any) error {
	return st.writeJSON(filepath.Join(st.Dir, name), v)
}

// LoadJournal reads the named journal into v. It reports false if there is none.
func (st Store) LoadJournal(name string, v any) (bool, error) {
	data, err := os.ReadFile(filepath.Join(st.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(data, v)
}

// HasJournal reports whether the named journal exists.
func (st Store) HasJournal(name string) bool {
	_, err := os.Stat(filepath.Join(st.Dir, name))
	return err == nil
}

// ClearJournal removes the named journal.
func (st Store) ClearJournal(name string) error {
	err := os.Remove(filepath.Join(st.Dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
