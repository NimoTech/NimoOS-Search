package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service/fileindex"
	"github.com/stretchr/testify/require"
)

type fakeFileSearcher struct {
	hits []fileindex.FileNameHit
	err  error
}

func (f fakeFileSearcher) Search(context.Context, string, int) ([]fileindex.FileNameHit, error) {
	return f.hits, f.err
}
func (f fakeFileSearcher) Status() string { return "ready" }

type fakeImageSearcher struct {
	hits []ImageHit
	err  error
}

func (f fakeImageSearcher) SmartSearch(context.Context, string, int, string) ([]ImageHit, error) {
	return f.hits, f.err
}

func newAggForTest(fi FileNameSearcher, im ImageSearcher) *Aggregator {
	search := &SearchService{
		Parser: &fakeParserA{},
		Qdrant: &fakeQdrantA{hits: []QdrantHit{{PointID: "p1", Score: 0.5, Payload: map[string]any{
			"file_id": "f1", "kind": "body", "chunk_no": int64(0), "text": strings.Repeat("hi ", 50),
		}}}},
		Cache: NewEmbedCache(10, 0), DefaultTopK: 5, RerankerCandidates: 40,
	}
	st := &SettingsStore{cur: SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15,
	}}
	return &Aggregator{Search: search, FileIndex: fi, Photos: im, Settings: st}
}

func TestAggregate_AllThreeGroups(t *testing.T) {
	agg := newAggForTest(
		fakeFileSearcher{hits: []fileindex.FileNameHit{{Path: "/DATA/x.pdf", Name: "x.pdf"}}},
		fakeImageSearcher{hits: []ImageHit{{AssetID: "a1", Name: "p.jpg"}}},
	)
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})
	require.Len(t, resp.Groups.Semantic, 1)
	require.Len(t, resp.Groups.Filenames, 1)
	require.Len(t, resp.Groups.Images, 1)
	require.Empty(t, resp.Warnings)
	require.Equal(t, "ready", resp.Stats["fileindex_status"])
}

func TestAggregate_SourcesNarrowing(t *testing.T) {
	agg := newAggForTest(
		fakeFileSearcher{hits: []fileindex.FileNameHit{{Path: "/DATA/x.pdf"}}},
		fakeImageSearcher{hits: []ImageHit{{AssetID: "a1"}}},
	)
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", Sources: []string{"images"}, AllowedRoots: []string{"r1"}})
	require.Empty(t, resp.Groups.Semantic)
	require.Empty(t, resp.Groups.Filenames)
	require.Len(t, resp.Groups.Images, 1)
}

func TestAggregate_ImageFailureDegrades(t *testing.T) {
	agg := newAggForTest(
		fakeFileSearcher{hits: []fileindex.FileNameHit{{Path: "/DATA/x.pdf"}}},
		fakeImageSearcher{err: errors.New("photos down")},
	)
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})
	require.Empty(t, resp.Groups.Images)
	require.Contains(t, resp.Warnings, "images_unavailable")
	require.Len(t, resp.Groups.Filenames, 1, "other groups unaffected")
}

func TestAggregate_NilDependenciesSkip(t *testing.T) {
	agg := newAggForTest(nil, nil)
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})
	require.Len(t, resp.Groups.Semantic, 1)
	require.Empty(t, resp.Groups.Filenames)
	require.Empty(t, resp.Groups.Images)
}

func TestAggregate_NoAccessibleRootsWarns(t *testing.T) {
	agg := newAggForTest(nil, nil)
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{}})
	require.Empty(t, resp.Groups.Semantic)
	require.Contains(t, resp.Warnings, "no_accessible_roots")
}

func TestAggregate_ReadsLiveSettings(t *testing.T) {
	agg := newAggForTest(
		fakeFileSearcher{hits: []fileindex.FileNameHit{{Path: "/DATA/x.pdf"}}},
		fakeImageSearcher{hits: []ImageHit{{AssetID: "a1"}}},
	)
	// change settings live → only images by default
	cur := agg.Settings.Get()
	cur.DefaultSources = []string{"images"}
	agg.Settings.cur = cur
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})
	require.Empty(t, resp.Groups.Semantic, "DefaultSources=[images] → semantic skipped when request omits sources")
	require.Len(t, resp.Groups.Images, 1)
}
