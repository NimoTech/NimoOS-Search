package fileindex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func openTmp(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}

func TestUpsertAndCount(t *testing.T) {
	idx := openTmp(t)
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/a/report.pdf", Name: "report.pdf", Ext: "pdf", Root: "/DATA", Size: 10, MtimeMs: 100}))
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/a/report.pdf", Name: "report.pdf", Ext: "pdf", Root: "/DATA", Size: 20, MtimeMs: 200}))
	n, err := idx.Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, n, "upsert on same path must replace, not duplicate")
}

func TestDeletePrefix_CascadesSubtree(t *testing.T) {
	idx := openTmp(t)
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/old", Name: "old", Root: "/DATA", IsDir: true}))
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/old/x.txt", Name: "x.txt", Ext: "txt", Root: "/DATA"}))
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/old2/y.txt", Name: "y.txt", Ext: "txt", Root: "/DATA"}))
	require.NoError(t, idx.DeletePrefix("/DATA/old"))
	n, _ := idx.Count(context.Background())
	require.Equal(t, 1, n, "/DATA/old and /DATA/old/* removed; /DATA/old2/* must survive (no false prefix match)")
}

func TestStatus(t *testing.T) {
	idx := openTmp(t)
	require.Equal(t, "scanning", idx.Status())
	idx.SetReady()
	require.Equal(t, "ready", idx.Status())
}
