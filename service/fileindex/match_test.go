package fileindex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenize(t *testing.T) {
	require.Equal(t, []string{"annual", "report", "2024"}, tokenize("Annual_Report 2024"))
	require.Equal(t, []string{"my", "file", "name"}, tokenize("MyFileName"))
	require.Equal(t, []string{"a"}, tokenize("  A  "))
	require.Empty(t, tokenize("   "))
}

func TestScoreName(t *testing.T) {
	// both terms hit > one term; full substring & prefix add weight
	two := scoreName("annual report 2024", []string{"annual", "report"})
	one := scoreName("annual report 2024", []string{"annual", "zzz"})
	require.Greater(t, two, one)
	require.Zero(t, scoreName("annual report", []string{"zzz"}))
}

func TestSearch_RanksAndFilters(t *testing.T) {
	idx := openTmp(t)
	for _, r := range []Record{
		{Path: "/DATA/annual_report_2024.pdf", Name: "annual_report_2024.pdf", Ext: "pdf", Root: "/DATA", MtimeMs: 300},
		{Path: "/DATA/report_old.pdf", Name: "report_old.pdf", Ext: "pdf", Root: "/DATA", MtimeMs: 100},
		{Path: "/DATA/notes.txt", Name: "notes.txt", Ext: "txt", Root: "/DATA", MtimeMs: 200},
	} {
		require.NoError(t, idx.Upsert(r))
	}
	hits, err := idx.Search(context.Background(), "annual report", 10)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	require.Equal(t, "/DATA/annual_report_2024.pdf", hits[0].Path, "two-term hit ranks first")
	for _, h := range hits {
		require.NotEqual(t, "/DATA/notes.txt", h.Path, "non-matching file excluded")
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	idx := openTmp(t)
	require.NoError(t, idx.Upsert(Record{Path: "/DATA/x.txt", Name: "x.txt", Root: "/DATA"}))
	hits, err := idx.Search(context.Background(), "   ", 10)
	require.NoError(t, err)
	require.Empty(t, hits)
}

var _ = filepath.Join // keep import if unused after edits
