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

func upsertPathForPurge(t *testing.T, idx *Index, path, name string) {
	t.Helper()
	require.NoError(t, idx.Upsert(Record{Path: path, Name: name, Ext: "txt"}))
}

func purgeHas(idx *Index, term string) bool {
	hits, _ := idx.Search(context.Background(), term, 50)
	return len(hits) > 0
}

func TestPurgeOutside_KeepsInsideDeletesOutside(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	upsertPathForPurge(t, idx, "/A/alpha.txt", "alpha.txt")
	upsertPathForPurge(t, idx, "/B/bravo.txt", "bravo.txt")
	require.NoError(t, idx.PurgeOutside([]string{"/A"}))
	require.True(t, purgeHas(idx, "alpha"))
	require.False(t, purgeHas(idx, "bravo"))
}

func TestPurgeOutside_KeepsSubdirDropsOuter(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	upsertPathForPurge(t, idx, "/A/outer.txt", "outer.txt")
	upsertPathForPurge(t, idx, "/A/sub/inner.txt", "inner.txt")
	require.NoError(t, idx.PurgeOutside([]string{"/A/sub"}))
	require.True(t, purgeHas(idx, "inner"))
	require.False(t, purgeHas(idx, "outer"))
}

func TestPurgeOutside_SiblingPrefixNotMatched(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	upsertPathForPurge(t, idx, "/A/keepme.txt", "keepme.txt")
	upsertPathForPurge(t, idx, "/AB/dropme.txt", "dropme.txt")
	require.NoError(t, idx.PurgeOutside([]string{"/A"}))
	require.True(t, purgeHas(idx, "keepme"))
	require.False(t, purgeHas(idx, "dropme"))
}

func TestPurgeOutside_CaseSensitive(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	upsertPathForPurge(t, idx, "/DATA/upper.txt", "upper.txt")
	upsertPathForPurge(t, idx, "/data/lower.txt", "lower.txt")
	require.NoError(t, idx.PurgeOutside([]string{"/DATA"}))
	require.True(t, purgeHas(idx, "upper"))
	require.False(t, purgeHas(idx, "lower"))
}

func TestPurgeOutside_EmptyClearsAll(t *testing.T) {
	idx, err := Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	upsertPathForPurge(t, idx, "/A/x.txt", "x.txt")
	require.NoError(t, idx.PurgeOutside(nil))
	n, _ := idx.Count(context.Background())
	require.Equal(t, 0, n)
}
