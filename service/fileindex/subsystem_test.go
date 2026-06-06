package fileindex

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
