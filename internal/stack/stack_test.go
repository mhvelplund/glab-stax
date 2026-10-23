package stack

import (
	"path/filepath"
	"testing"

	"github.com/mhvelplund/glab-stax/internal/exitcode"
)

func sample() *File {
	return &File{Stacks: []*Stack{
		{Trunk: "main", Remote: "origin", Branches: []*Branch{{Name: "a"}, {Name: "b", Merged: true}, {Name: "c"}}},
		{Trunk: "main", Remote: "origin", Branches: []*Branch{{Name: "x"}}},
		{Trunk: "dev", Remote: "origin", Branches: []*Branch{{Name: "y"}}},
	}}
}

func TestParentAndActive(t *testing.T) {
	s := sample().Stacks[0]
	if p := s.Parent("a"); p != "main" {
		t.Errorf("Parent(a) = %q", p)
	}
	if p := s.Parent("c"); p != "a" { // b is merged
		t.Errorf("Parent(c) = %q", p)
	}
	var names []string
	for _, b := range s.Active() {
		names = append(names, b.Name)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "c" {
		t.Errorf("Active = %v", names)
	}
	if s.Top().Name != "c" || s.Bottom().Name != "a" {
		t.Errorf("Top/Bottom = %s/%s", s.Top().Name, s.Bottom().Name)
	}
}

func TestResolve(t *testing.T) {
	f := sample()
	if s, err := f.Resolve("c"); err != nil || s != f.Stacks[0] {
		t.Fatalf("Resolve(c) = %v, %v", s, err)
	}
	if s, err := f.Resolve("dev"); err != nil || s != f.Stacks[2] {
		t.Fatalf("Resolve(dev) = %v, %v", s, err)
	}
	if _, err := f.Resolve("main"); exitcode.Of(err) != exitcode.Ambiguous {
		t.Fatalf("Resolve(main) err = %v", err)
	}
	if _, err := f.Resolve("zzz"); exitcode.Of(err) != exitcode.NotInStack {
		t.Fatalf("Resolve(zzz) err = %v", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := Store{Dir: dir}
	f, err := st.Load()
	if err != nil || len(f.Stacks) != 0 {
		t.Fatalf("Load empty = %v, %v", f, err)
	}
	f = sample()
	f.Stacks[0].Branches[0].MR = &MR{IID: 7, URL: "https://gitlab.example/p/-/merge_requests/7"}
	if err := st.Save(f); err != nil {
		t.Fatal(err)
	}
	got, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Stacks) != 3 || got.Stacks[0].Branches[0].MR.IID != 7 || got.Version != FormatVersion {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if _, err := filepath.Glob(filepath.Join(dir, "glab-stax.tmp*")); err != nil {
		t.Fatal(err)
	}
}

func TestLock(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	unlock, err := st.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Lock(); exitcode.Of(err) != exitcode.Locked {
		t.Fatalf("second Lock err = %v", err)
	}
	unlock()
	unlock2, err := st.Lock()
	if err != nil {
		t.Fatalf("Lock after unlock: %v", err)
	}
	unlock2()
}

func TestStaleLockIsReclaimed(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	if err := writeLock(st.lockPath(), 999999999); err != nil {
		t.Fatal(err)
	}
	unlock, err := st.Lock()
	if err != nil {
		t.Fatalf("stale lock not reclaimed: %v", err)
	}
	unlock()
}

func TestRemoveStack(t *testing.T) {
	f := sample()
	f.Remove(f.Stacks[1])
	if len(f.Stacks) != 2 || f.Stacks[1].Trunk != "dev" {
		t.Fatalf("Remove left %+v", f.Stacks)
	}
}

func TestJournal(t *testing.T) {
	st := Store{Dir: t.TempDir()}
	var v struct{ N int }
	if ok, err := st.LoadJournal(RebaseJournal, &v); ok || err != nil {
		t.Fatalf("empty LoadJournal = %v, %v", ok, err)
	}
	if err := st.SaveJournal(RebaseJournal, struct{ N int }{3}); err != nil {
		t.Fatal(err)
	}
	if !st.HasJournal(RebaseJournal) {
		t.Fatal("HasJournal = false")
	}
	if ok, err := st.LoadJournal(RebaseJournal, &v); !ok || err != nil || v.N != 3 {
		t.Fatalf("LoadJournal = %v, %v, %+v", ok, err, v)
	}
	if err := st.ClearJournal(RebaseJournal); err != nil || st.HasJournal(RebaseJournal) {
		t.Fatalf("ClearJournal: %v", err)
	}
	if err := st.ClearJournal(RebaseJournal); err != nil {
		t.Fatalf("ClearJournal twice: %v", err)
	}
}
