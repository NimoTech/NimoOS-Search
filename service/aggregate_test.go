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

// capturingQdrant records the root filter condition received by the
// semantic source's SearchTextHybrid, used to assert that ApplyScope's
// result really made it into the Qdrant query (not just the internal
// Filters struct).
type capturingQdrant struct {
	hits       []QdrantHit
	gotRootIDs []string
}

func (c *capturingQdrant) SearchTextHybrid(_ context.Context, r QdrantSearchRequest) ([]QdrantHit, error) {
	if r.Filter != nil {
		c.gotRootIDs = r.Filter.RootIDsAny
	}
	return c.hits, nil
}
func (c *capturingQdrant) ScrollByFileID(context.Context, string, string, []string, int, string) ([]QdrantHit, string, error) {
	return nil, "", nil
}
func (c *capturingQdrant) Count(context.Context, string) (uint64, error) { return 0, nil }

// TestAggregate_PhotosRootPassesScope is the final acceptance point of the
// "authorization source decoupling" project: when the allowed set contains
// the core seed root "photos" (request doesn't explicitly specify
// root_ids), the ApplyScope call in aggregate.go's semantic source
// shouldn't filter it out - i.e. a caption in text_chunks with
// root_ids=["photos"] can still be hit by semantic search.
func TestAggregate_PhotosRootPassesScope(t *testing.T) {
	q := &capturingQdrant{hits: []QdrantHit{
		{PointID: "p1", Score: 0.8, Payload: map[string]any{
			"file_id": "photos", "kind": "caption", "chunk_no": int64(0), "text": "a cat on a windowsill",
		}},
	}}
	search := &SearchService{
		Parser: &fakeParserA{}, Qdrant: q,
		Cache: NewEmbedCache(10, 0), DefaultTopK: 5, RerankerCandidates: 40,
	}
	st := &SettingsStore{cur: SearchSettings{
		DefaultSources: []string{"semantic"}, SemanticTopK: 5, MaxTotalResults: 15,
	}}
	agg := &Aggregator{Search: search, Settings: st}

	resp := agg.Aggregate(context.Background(), AggregateRequest{
		Query: "cat", AllowedRoots: []string{"photos"},
	})

	require.Empty(t, resp.Warnings, "photos should not be blocked by no_accessible_roots")
	require.Contains(t, q.gotRootIDs, "photos", "ApplyScope should put photos into the semantic query's root filter")
	require.Len(t, resp.Groups.Semantic, 1)
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

type fakeNotesSearcher struct {
	hits []NoteHit
	err  error
}

func (f fakeNotesSearcher) Query(context.Context, string, int, string) ([]NoteHit, error) {
	return f.hits, f.err
}

func TestAggregate_NotesGroupIncluded(t *testing.T) {
	agg := newAggForTest(nil, nil)
	agg.Notes = fakeNotesSearcher{hits: []NoteHit{{NoteID: "n1", Score: 0.9}}}
	agg.Settings.cur.DefaultSources = []string{"semantic", "notes"}
	agg.Settings.cur.NotesTopK = 5
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "q", UserID: "1"})
	require.Len(t, resp.Groups.Notes, 1)
	require.Equal(t, "n1", resp.Groups.Notes[0].NoteID)
}

func TestAggregate_NotesFailureDegrades(t *testing.T) {
	agg := newAggForTest(nil, nil)
	agg.Notes = fakeNotesSearcher{err: context.DeadlineExceeded}
	agg.Settings.cur.DefaultSources = []string{"notes"}
	agg.Settings.cur.NotesTopK = 5
	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "q", UserID: "1"})
	require.Contains(t, resp.Warnings, "notes_unavailable")
	require.Empty(t, resp.Groups.Notes)
}

// fakeCaptions implements CaptionSource: looks up a caption by assetID from
// a map, or always returns err (used to simulate a Qdrant point-lookup
// failure and verify fail-open).
type fakeCaptions struct {
	m   map[string]string
	err error
}

func (f *fakeCaptions) PhotoCaption(_ context.Context, id string, _ []string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.m[id], nil
}

func TestAggregate_ImagesCarryCaption(t *testing.T) {
	agg := newAggForTest(nil, fakeImageSearcher{hits: []ImageHit{
		{AssetID: "a1", Name: "dam.jpg"},
		{AssetID: "a2", Name: "cat.jpg"},
	}})
	agg.Captions = &fakeCaptions{m: map[string]string{"a1": "A large dam across a river valley"}}

	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})

	require.Len(t, resp.Groups.Images, 2)
	byID := map[string]ImageHit{}
	for _, h := range resp.Groups.Images {
		byID[h.AssetID] = h
	}
	require.Equal(t, "A large dam across a river valley", byID["a1"].Caption)
	require.Empty(t, byID["a2"].Caption, "no caption hit should not error, just leave it blank")
}

func TestAggregate_ImagesCaptionFailOpen(t *testing.T) {
	agg := newAggForTest(nil, fakeImageSearcher{hits: []ImageHit{
		{AssetID: "a1", Name: "dam.jpg"},
		{AssetID: "a2", Name: "cat.jpg"},
	}})
	agg.Captions = &fakeCaptions{err: errors.New("qdrant down")}

	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})

	require.Len(t, resp.Groups.Images, 2, "fail-open: a caption query failure must not affect the hit count")
	for _, h := range resp.Groups.Images {
		require.Empty(t, h.Caption)
	}
	for _, w := range resp.Warnings {
		require.NotContains(t, w, "caption", "fail-open: should not add a caption-related warning")
	}
}

func TestAggregate_ImagesCaptionTruncated(t *testing.T) {
	long := strings.Repeat("测", 150) + strings.Repeat("a", 150) // 300 runes, includes multi-byte characters
	agg := newAggForTest(nil, fakeImageSearcher{hits: []ImageHit{{AssetID: "a1", Name: "dam.jpg"}}})
	agg.Captions = &fakeCaptions{m: map[string]string{"a1": long}}

	resp := agg.Aggregate(context.Background(), AggregateRequest{Query: "x", AllowedRoots: []string{"r1"}})

	require.Len(t, resp.Groups.Images, 1)
	caption := resp.Groups.Images[0].Caption
	runes := []rune(caption)
	require.LessOrEqual(t, len(runes), 201)
	require.True(t, strings.HasSuffix(caption, "…"))
}
