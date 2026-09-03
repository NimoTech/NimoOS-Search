package fileindex

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func seedScoped(t *testing.T) *Index {
	t.Helper()
	idx := openTmp(t)
	for _, p := range []string{"/DATA/a/x_report.txt", "/DATA/ab/trick_report.txt", "/DATA/b/y_report.txt", "/mnt/usb/z_report.txt"} {
		require.NoError(t, idx.Upsert(Record{Path: p, Name: "report", Ext: "txt", Root: "/DATA"}))
	}
	return idx
}

func paths(hits []FileNameHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.Path)
	}
	return out
}

func TestSearchWithin_OnlyReturnsFilesUnderScope(t *testing.T) {
	idx := seedScoped(t)
	hits, err := idx.SearchWithin(context.Background(), "report", 10, []string{"/DATA/a"})
	require.NoError(t, err)
	require.Equal(t, []string{"/DATA/a/x_report.txt"}, paths(hits), "/DATA/ab must not match the /DATA/a prefix")
}

func TestSearchWithin_MultipleScopes(t *testing.T) {
	idx := seedScoped(t)
	hits, err := idx.SearchWithin(context.Background(), "report", 10, []string{"/DATA/b", "/mnt/usb/"})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"/DATA/b/y_report.txt", "/mnt/usb/z_report.txt"}, paths(hits))
}

func TestSearchWithin_EmptyScope_FailsClosed(t *testing.T) {
	idx := seedScoped(t)
	hits, err := idx.SearchWithin(context.Background(), "report", 10, nil)
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestSearchWithin_TopKAppliesAfterScope(t *testing.T) {
	idx := seedScoped(t)
	hits, err := idx.SearchWithin(context.Background(), "report", 1, []string{"/DATA"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Path, "/DATA/")
}
