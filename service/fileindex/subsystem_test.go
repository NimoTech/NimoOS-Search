package fileindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTmpFile(t *testing.T, dir, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0644))
}

func subSearchHas(idx *Index, term string) bool {
	hits, _ := idx.Search(context.Background(), term, 50)
	return len(hits) > 0
}

func requireSearchEventually(t *testing.T, idx *Index, term string, want bool) {
	t.Helper()
	require.Eventually(t, func() bool { return subSearchHas(idx, term) == want },
		3*time.Second, 20*time.Millisecond, "search %q want present=%v", term, want)
}

func TestSubsystem_Report(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	idx.SetReady()
	w := NewWatcher(idx, []string{"/DATA"})
	sub := &Subsystem{Index: idx, Watcher: w, Roots: []string{"/DATA"}}
	rep := sub.Report()
	require.Equal(t, "ready", rep.Status)
	require.False(t, rep.WatchDegraded)
	require.Equal(t, 0, rep.IndexedCount)
}

func TestSubsystem_NilIndexDisabled(t *testing.T) {
	var sub *Subsystem
	rep := sub.Report()
	require.Equal(t, "disabled", rep.Status)
}

func TestSubsystem_ReloadRoots_PurgesRemovedScansAdded(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeTmpFile(t, dirA, "alpha.txt")
	writeTmpFile(t, dirB, "bravo.txt")
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	sub := NewSubsystem(idx, time.Hour, time.Hour, nil)
	t.Cleanup(func() { _ = sub.Close() })
	sub.StartInitial([]string{dirA})
	requireSearchEventually(t, idx, "alpha", true)
	requireSearchEventually(t, idx, "bravo", false)
	sub.ReloadRoots([]string{dirB})
	requireSearchEventually(t, idx, "bravo", true)
	requireSearchEventually(t, idx, "alpha", false)
	require.Equal(t, []string{filepath.Clean(dirB)}, sub.Roots)
}

func TestSubsystem_ReloadRoots_RapidConverges(t *testing.T) {
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()
	writeTmpFile(t, dirA, "aaa.txt")
	writeTmpFile(t, dirB, "bbb.txt")
	writeTmpFile(t, dirC, "ccc.txt")
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	sub := NewSubsystem(idx, time.Hour, time.Hour, nil)
	t.Cleanup(func() { _ = sub.Close() })
	sub.StartInitial([]string{dirA})
	sub.ReloadRoots([]string{dirB})
	sub.ReloadRoots([]string{dirC})
	requireSearchEventually(t, idx, "ccc", true)
	requireSearchEventually(t, idx, "aaa", false)
	requireSearchEventually(t, idx, "bbb", false)
	require.Equal(t, []string{filepath.Clean(dirC)}, sub.Roots)
}

func TestSubsystem_ReloadRoots_NormalizesTrailingSlash(t *testing.T) {
	dirA := t.TempDir()
	writeTmpFile(t, dirA, "alpha.txt")
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	sub := NewSubsystem(idx, time.Hour, time.Hour, nil)
	t.Cleanup(func() { _ = sub.Close() })
	sub.StartInitial([]string{dirA})
	requireSearchEventually(t, idx, "alpha", true)
	sub.ReloadRoots([]string{dirA + "/"})
	requireSearchEventually(t, idx, "alpha", true)
	require.Equal(t, []string{filepath.Clean(dirA)}, sub.Roots)
}

func TestSubsystem_ReloadRoots_DisabledRecordsRoots(t *testing.T) {
	var sub = &Subsystem{}
	sub.ReloadRoots([]string{"/DATA/"})
	require.Equal(t, []string{"/DATA"}, sub.Roots)
	require.Equal(t, "disabled", sub.Report().Status)
}

func TestSubsystem_CloseIdempotent(t *testing.T) {
	dirA := t.TempDir()
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	sub := NewSubsystem(idx, time.Hour, time.Hour, nil)
	sub.StartInitial([]string{dirA})
	require.NoError(t, sub.Close())
	require.NoError(t, sub.Close())
}
