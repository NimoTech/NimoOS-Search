package fileindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootScan_IndexesTreeSkippingHidden(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "alpha.txt"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "beta.md"), []byte("y"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden"), []byte("z"), 0644))

	idx := openTmp(t)
	require.NoError(t, idx.BootScan([]string{root}))

	hits, err := idx.Search(context.Background(), "beta", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "md", hits[0].Ext)

	h, _ := idx.Search(context.Background(), "hidden", 10)
	require.Empty(t, h, "dotfiles are skipped")
}

func TestReconcile_AddsAndRemoves(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0644))
	stale := filepath.Join(root, "stale.txt")
	require.NoError(t, os.WriteFile(stale, []byte("x"), 0644))

	idx := openTmp(t)
	require.NoError(t, idx.BootScan([]string{root}))
	require.NoError(t, os.Remove(stale))
	require.NoError(t, idx.Reconcile(root))

	n, _ := idx.Count(context.Background())
	require.Equal(t, 1, n, "reconcile drops the removed file")
}
