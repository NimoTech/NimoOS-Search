package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRankOf_FirstMatchingPositionAcrossSources(t *testing.T) {
	c := Case{ID: "q1", Expect: []Expect{{PathContains: "125u.csv"}}}
	groups := map[string][]candidate{
		"semantic":  {{paths: []string{"/DATA/a.csv"}}, {paths: []string{"/DATA/b.csv"}}, {paths: []string{"/DATA/Intel 125U.csv"}}},
		"filenames": {{paths: []string{"/DATA/x.csv"}}, {paths: []string{"/DATA/Intel 125U.csv"}}},
	}
	rank, src, matched := rankOf(c, groups)
	require.Equal(t, 2, rank, "best rank over all sources")
	require.Equal(t, "filenames", src)
	require.Equal(t, "/DATA/Intel 125U.csv", matched)
}

func TestRankOf_CaseInsensitiveAndFileID(t *testing.T) {
	c := Case{ID: "q1", Expect: []Expect{{FileID: "photos:abc"}}}
	groups := map[string][]candidate{"semantic": {{fileID: "x"}, {fileID: "PHOTOS:ABC"}}}
	rank, _, _ := rankOf(c, groups)
	require.Equal(t, 2, rank)
	c2 := Case{Expect: []Expect{{PathContains: "WHITEBOARD/2026"}}}
	rank, _, _ = rankOf(c2, map[string][]candidate{"semantic": {{paths: []string{"/DATA/whiteboard/2026-08/a.jpg"}}}})
	require.Equal(t, 1, rank)
}

func TestRankOf_MissIsZero(t *testing.T) {
	c := Case{Expect: []Expect{{PathContains: "nope"}}}
	rank, src, _ := rankOf(c, map[string][]candidate{"semantic": {{paths: []string{"/a"}}}})
	require.Equal(t, 0, rank)
	require.Equal(t, "", src)
}

func TestSummarize_RecallAndMRR(t *testing.T) {
	rs := []CaseResult{
		{ID: "a", Rank: 1, LatencyMs: 100},
		{ID: "b", Rank: 3, LatencyMs: 200},
		{ID: "c", Rank: 12, LatencyMs: 300}, // beyond 10: counts as miss for all @k metrics
		{ID: "d", Rank: 0, LatencyMs: 400},
	}
	s := summarize(rs)
	require.Equal(t, 4, s.N)
	require.InDelta(t, 0.25, s.Recall1, 1e-9)
	require.InDelta(t, 0.5, s.Recall5, 1e-9)
	require.InDelta(t, 0.5, s.Recall10, 1e-9)
	require.InDelta(t, (1.0+1.0/3)/4, s.MRR10, 1e-9)
	require.Equal(t, 200, s.P50Ms)
	require.Equal(t, 400, s.P95Ms)
}

func TestCompare_GainedLostAndMoved(t *testing.T) {
	old := &Results{Cases: []CaseResult{{ID: "a", Rank: 1}, {ID: "b", Rank: 0}, {ID: "c", Rank: 2}, {ID: "d", Rank: 4}}}
	cur := &Results{Cases: []CaseResult{{ID: "a", Rank: 0}, {ID: "b", Rank: 3}, {ID: "c", Rank: 2}, {ID: "d", Rank: 1}}}
	d := compare(old, cur)
	require.Equal(t, []string{"b"}, d.Gained)
	require.Equal(t, []string{"a"}, d.Lost)
	require.Len(t, d.Moved, 1)
	require.Equal(t, "d", d.Moved[0].ID)
	require.Equal(t, 4, d.Moved[0].Old)
	require.Equal(t, 1, d.Moved[0].New)
}

func TestPercentile_Bounds(t *testing.T) {
	require.Equal(t, 0, percentile(nil, 50))
	require.Equal(t, 7, percentile([]int{7}, 95))
	require.Equal(t, 3, percentile([]int{1, 2, 3, 4, 5}, 50))
}
