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

func TestBootScan_SkipsExcludedSubtree(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "user"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "user", "alpha.txt"), []byte("x"), 0644))
	// A system mount living under the scan root that must never be indexed.
	sysmount := filepath.Join(root, "root-ro")
	require.NoError(t, os.MkdirAll(filepath.Join(sysmount, "etc"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sysmount, "etc", "sysfile.txt"), []byte("y"), 0644))

	idx := openTmp(t)
	idx.SetExcludes([]string{sysmount})
	require.NoError(t, idx.BootScan([]string{root}))

	hits, err := idx.Search(context.Background(), "alpha", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1, "user file under the root is indexed")

	h, _ := idx.Search(context.Background(), "sysfile", 10)
	require.Empty(t, h, "files under an excluded subtree are not indexed")
}

func TestReconcile_DropsRowsUnderNewlyExcludedSubtree(t *testing.T) {
	root := t.TempDir()
	sysmount := filepath.Join(root, "root-ro")
	require.NoError(t, os.MkdirAll(sysmount, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.txt"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(sysmount, "sysfile.txt"), []byte("y"), 0644))

	idx := openTmp(t)
	require.NoError(t, idx.BootScan([]string{root})) // indexes everything, no excludes yet
	pre, _ := idx.Search(context.Background(), "sysfile", 10)
	require.Len(t, pre, 1, "precondition: excluded file was indexed before exclusion")

	idx.SetExcludes([]string{sysmount})
	require.NoError(t, idx.Reconcile(root))

	h, _ := idx.Search(context.Background(), "sysfile", 10)
	require.Empty(t, h, "reconcile drops rows that fell into an excluded subtree")
	keep, _ := idx.Search(context.Background(), "keep", 10)
	require.Len(t, keep, 1, "retained files survive reconcile")
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
