package fileindex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
)

func TestHandleEvent_RenameDirCascades(t *testing.T) {
	idx := openTmp(t)
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/old", Name: "old", Root: "/DATA", IsDir: true}))
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/old/a.txt", Name: "a.txt", Root: "/DATA"}))
	w := NewWatcher(idx, []string{"/DATA"})
	w.handleEvent(fsnotify.Rename, "/DATA/old")
	n, _ := idx.Count(context.Background())
	require.Equal(t, 0, n, "renaming a dir cascades-deletes its subtree from the index")
}

func TestHandleEvent_CreateFileUpserts(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "fresh.log")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	idx := openTmp(t)
	w := NewWatcher(idx, []string{root})
	w.handleEvent(fsnotify.Create, f)
	hits, _ := idx.Search(context.Background(), "fresh", 10)
	require.Len(t, hits, 1)
}

func TestAddWatchRecursive_DegradesOnLimit(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0755))
	idx := openTmp(t)
	w := NewWatcher(idx, []string{root})
	degraded := false
	w.onDegrade = func() { degraded = true }
	w.add = func(string) error { return &os.SyscallError{Syscall: "inotify_add_watch", Err: syscall.ENOSPC} }
	w.addWatchRecursive(root)
	require.True(t, w.Degraded())
	require.True(t, degraded, "onDegrade callback fired")
}

func TestIsWatchLimit(t *testing.T) {
	require.True(t, isWatchLimit(&os.SyscallError{Err: syscall.ENOSPC}))
	require.False(t, isWatchLimit(errors.New("other")))
}

func TestRootOf_SeparatorBoundary(t *testing.T) {
	w := NewWatcher(nil, []string{"/DATA"})
	require.Equal(t, "/DATA", w.rootOf("/DATA/x.txt"))
	require.Equal(t, "/DATA", w.rootOf("/DATA"))
	require.Equal(t, "", w.rootOf("/DATABASE/x.txt"))
	require.Equal(t, "", w.rootOf("/DATAfoo"))
}

func TestHandleEvent_CreateDirIndexesItself(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "newdir")
	require.NoError(t, os.Mkdir(d, 0755))
	idx := openTmp(t)
	w := NewWatcher(idx, []string{root})
	w.handleEvent(fsnotify.Create, d)
	hits, err := idx.Search(context.Background(), "newdir", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.True(t, hits[0].IsDir)
}
