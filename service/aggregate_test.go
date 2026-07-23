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

// capturingQdrant 记录 semantic 源 SearchTextHybrid 收到的 root 过滤条件,
// 用于断言 ApplyScope 的结果真的被送进了 Qdrant 查询(而不仅仅是内部的
// Filters 结构体)。
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

// TestAggregate_PhotosRootPassesScope 是"授权源解耦"项目的最终验收点:
// allowed 集里含核心 seed root "photos"(请求不显式指定 root_ids)时,
// aggregate.go semantic 源里的 ApplyScope 调用不应把它过滤掉——
// 即 text_chunks 里 root_ids=["photos"] 的 caption 能被语义检索命中。
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

	require.Empty(t, resp.Warnings, "photos 不应被 no_accessible_roots 挡掉")
	require.Contains(t, q.gotRootIDs, "photos", "ApplyScope 应把 photos 放进 semantic 查询的 root 过滤")
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
