package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeParser stubs ParserClient for orchestration tests
type fakeParser struct {
	embedCalls   int
	rerankErr    error
	rerankScores []RerankScore
	expandFiles  []FileRecord
}

func (f *fakeParser) Embed(ctx context.Context, model, t, text, b64 string) (*EmbedResult, error) {
	f.embedCalls++
	return &EmbedResult{Dense: []float32{0.1, 0.2}, Sparse: &Sparse{Indices: []int{1}, Values: []float32{0.5}},
		Dim: 2, ModelVersion: "bge-m3/v1"}, nil
}
func (f *fakeParser) Rerank(ctx context.Context, q string, c []RerankCandidate, k *int) (*RerankResult, error) {
	if f.rerankErr != nil {
		return nil, f.rerankErr
	}
	return &RerankResult{Scores: f.rerankScores, ModelVersion: "bge-reranker-v2-m3/v1"}, nil
}
func (f *fakeParser) ExpandFiles(ctx context.Context, ids []string) (*ExpandFilesResult, error) {
	return &ExpandFilesResult{Files: f.expandFiles}, nil
}

type fakeQdrant struct {
	hits []QdrantHit
	// distinct is what DistinctValues returns (the facet over payload.mime);
	// distinctErr makes the facet fail; distinctCalls counts facet round-trips
	// so the TTL cache can be asserted.
	distinct      []string
	distinctErr   error
	distinctCalls int
	searchCalls   int
	lastReq       *QdrantSearchRequest
}

func (f *fakeQdrant) SearchTextHybrid(ctx context.Context, r QdrantSearchRequest) ([]QdrantHit, error) {
	f.searchCalls++
	rr := r
	f.lastReq = &rr
	return f.hits, nil
}
func (f *fakeQdrant) DistinctValues(ctx context.Context, collection, key string) ([]string, error) {
	f.distinctCalls++
	if f.distinctErr != nil {
		return nil, f.distinctErr
	}
	return f.distinct, nil
}
func (f *fakeQdrant) ScrollByFileID(ctx context.Context, c, fid string, roots []string, lim int, off string) ([]QdrantHit, string, error) {
	return nil, "", nil
}
func (f *fakeQdrant) Count(ctx context.Context, c string) (uint64, error) { return 0, nil }

func TestSearchText_RerankAppliedAndPathsExpanded(t *testing.T) {
	p := &fakeParser{
		rerankScores: []RerankScore{{ID: "p1", Score: 0.9}, {ID: "p2", Score: 0.3}},
		expandFiles: []FileRecord{
			{FileID: "f1", Paths: []FilePath{{RootID: "r1", Path: "/a.md"}}, Mime: "text/markdown"},
		},
	}
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "p1", Score: 0.5, Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(7), "text": "hello world"}},
		{PointID: "p2", Score: 0.4, Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(8), "text": "later"}},
	}}
	svc := &SearchService{
		Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour),
		ParserVersion: "parser/0.1.0", DefaultTopK: 5, RerankerCandidates: 40,
	}
	resp, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "hi", Filters: &Filters{RootIDs: []string{"r1"}}, TopK: 5, Rerank: true,
	})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 2)
	// Rerank applied: p1 (0.9) > p2 (0.3)
	require.Equal(t, "p1", resp.Hits[0].PointID)
	require.InDelta(t, 0.9, resp.Hits[0].Score, 1e-6)
	require.InDelta(t, 0.5, resp.Hits[0].RawScore, 1e-6)
	// Paths expanded
	require.Equal(t, "/a.md", resp.Hits[0].Paths[0].Path)
	require.Empty(t, resp.Warnings)
}

func TestSearchText_RerankFailureFallsBackToRawScore(t *testing.T) {
	p := &fakeParser{rerankErr: ErrRerankerUnavailable}
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "p1", Score: 0.5, Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1), "text": "x"}},
	}}
	svc := &SearchService{
		Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour),
		ParserVersion: "parser/0.1.0", DefaultTopK: 5, RerankerCandidates: 40,
	}
	resp, err := svc.SearchText(context.Background(), SearchRequest{Query: "hi", Rerank: true,
		Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Contains(t, resp.Warnings, "rerank_unavailable")
	require.InDelta(t, 0.5, resp.Hits[0].Score, 1e-6) // raw_score reused
}

func TestSearchText_EmbedderDownReturns503(t *testing.T) {
	p := &errParser{err: ErrEmbedderUnavailable}
	q := &fakeQdrant{}
	svc := &SearchService{Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour), ParserVersion: "v"}
	_, err := svc.SearchText(context.Background(), SearchRequest{Query: "hi",
		Filters: &Filters{RootIDs: []string{"r1"}}})
	require.ErrorIs(t, err, ErrEmbedderUnavailable)
}

type errParser struct{ err error }

func (e *errParser) Embed(ctx context.Context, m, t, x, b string) (*EmbedResult, error) {
	return nil, e.err
}
func (e *errParser) Rerank(ctx context.Context, q string, c []RerankCandidate, k *int) (*RerankResult, error) {
	return nil, errors.New("not called")
}
func (e *errParser) ExpandFiles(ctx context.Context, ids []string) (*ExpandFilesResult, error) {
	return nil, errors.New("not called")
}

func TestSearchText_GroupByFile(t *testing.T) {
	p := &fakeParser{
		rerankScores: []RerankScore{
			{ID: "a1", Score: 0.9}, {ID: "a2", Score: 0.7}, {ID: "a3", Score: 0.6},
			{ID: "b1", Score: 0.8}, {ID: "c1", Score: 0.5},
		},
		expandFiles: []FileRecord{
			{FileID: "fa", Paths: []FilePath{{RootID: "r1", Path: "/a.pdf"}}, Mime: "application/pdf"},
			{FileID: "fb", Paths: []FilePath{{RootID: "r1", Path: "/b.md"}}, Mime: "text/markdown"},
			{FileID: "fc", Paths: []FilePath{{RootID: "r1", Path: "/c.txt"}}, Mime: "text/plain"},
		},
	}
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "a1", Score: 0.1, Payload: map[string]any{"file_id": "fa", "kind": "body", "chunk_no": int64(1), "text": "a-one"}},
		{PointID: "a2", Score: 0.1, Payload: map[string]any{"file_id": "fa", "kind": "body", "chunk_no": int64(2), "text": "a-two"}},
		{PointID: "a3", Score: 0.1, Payload: map[string]any{"file_id": "fa", "kind": "body", "chunk_no": int64(3), "text": "a-three"}},
		{PointID: "b1", Score: 0.1, Payload: map[string]any{"file_id": "fb", "kind": "body", "chunk_no": int64(1), "text": "b-one"}},
		{PointID: "c1", Score: 0.1, Payload: map[string]any{"file_id": "fc", "kind": "body", "chunk_no": int64(1), "text": "c-one"}},
	}}
	svc := &SearchService{
		Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour),
		ParserVersion: "v", DefaultTopK: 10, RerankerCandidates: 40,
	}
	resp, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "hi", Filters: &Filters{RootIDs: []string{"r1"}}, TopK: 2, Rerank: true,
		GroupByFile: true, MaxChunksPerFile: 2,
	})
	require.NoError(t, err)
	require.Len(t, resp.Files, 2)
	require.Equal(t, "fa", resp.Files[0].FileID)
	require.InDelta(t, 0.9, resp.Files[0].Score, 1e-6)
	require.Equal(t, "/a.pdf", resp.Files[0].Paths[0].Path)
	require.Equal(t, "application/pdf", resp.Files[0].Mime)
	require.Len(t, resp.Files[0].Chunks, 2)
	require.InDelta(t, 0.9, resp.Files[0].Chunks[0].Score, 1e-6)
	require.InDelta(t, 0.7, resp.Files[0].Chunks[1].Score, 1e-6)
	require.Equal(t, "fb", resp.Files[1].FileID)
	require.Len(t, resp.Files[1].Chunks, 1)
}

func TestSearchText_GroupByFileWithoutRerank(t *testing.T) {
	p := &fakeParser{
		expandFiles: []FileRecord{
			{FileID: "fa", Paths: []FilePath{{RootID: "r1", Path: "/a.pdf"}}, Mime: "application/pdf"},
			{FileID: "fb", Paths: []FilePath{{RootID: "r1", Path: "/b.md"}}, Mime: "text/markdown"},
		},
	}
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "a1", Score: 0.9, Payload: map[string]any{"file_id": "fa", "kind": "body", "chunk_no": int64(1), "text": "a-one"}},
		{PointID: "b1", Score: 0.7, Payload: map[string]any{"file_id": "fb", "kind": "body", "chunk_no": int64(1), "text": "b-one"}},
	}}
	svc := &SearchService{Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour), ParserVersion: "v", DefaultTopK: 10, RerankerCandidates: 40}
	resp, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "hi", Filters: &Filters{RootIDs: []string{"r1"}}, TopK: 5, Rerank: false, GroupByFile: true,
	})
	require.NoError(t, err)
	require.Len(t, resp.Files, 2)
	require.Equal(t, "fa", resp.Files[0].FileID) // raw score 0.9 ranks first
	require.InDelta(t, 0.9, resp.Files[0].Score, 1e-6)
	require.Equal(t, "fb", resp.Files[1].FileID)
}

func TestSearchText_NoGroupingKeepsFlatHits(t *testing.T) {
	p := &fakeParser{
		rerankScores: []RerankScore{{ID: "p1", Score: 0.9}, {ID: "p2", Score: 0.3}},
		expandFiles:  []FileRecord{{FileID: "f1", Paths: []FilePath{{RootID: "r1", Path: "/a.md"}}}},
	}
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "p1", Score: 0.5, Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1), "text": "x"}},
		{PointID: "p2", Score: 0.4, Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(2), "text": "y"}},
	}}
	svc := &SearchService{Parser: p, Qdrant: q, Cache: NewEmbedCache(10, time.Hour), ParserVersion: "v", DefaultTopK: 5, RerankerCandidates: 40}
	resp, err := svc.SearchText(context.Background(), SearchRequest{Query: "hi", Filters: &Filters{RootIDs: []string{"r1"}}, TopK: 5, Rerank: true})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 2)
	require.Nil(t, resp.Files)
}

// --- mime_prefix semantics -------------------------------------------------
//
// Qdrant's keyword index has no prefix match, so the service expands each
// prefix (an entry ending in "/") into the set of exact mime values currently
// present in the collection (facet over payload.mime). Entries without a
// trailing slash are exact mimes and pass through untouched — that keeps the
// UI's per-type checkboxes ("text/markdown" must NOT also select
// "text/markdown+docling/pdf") byte-for-byte unchanged.

func knownMimes() []string {
	return []string{"video/mp4", "text/markdown", "text/markdown+docling/pdf", "image/png", "text/plain"}
}

func TestSearchText_MimePrefixWithTrailingSlashExpandsToKnownMimes(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes()}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	_, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "x", Filters: &Filters{MimePrefix: []string{"text/"}},
	})
	require.NoError(t, err)
	require.NotNil(t, q.lastReq)
	require.Equal(t, []string{"text/markdown", "text/markdown+docling/pdf", "text/plain"}, q.lastReq.Filter.MimeIn)
}

func TestSearchText_ExactMimeIsNotTreatedAsPrefix(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes()}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	_, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "x", Filters: &Filters{MimePrefix: []string{"text/markdown", "application/pdf"}},
	})
	require.NoError(t, err)
	// "text/markdown" must not swallow "text/markdown+docling/pdf"; an exact
	// value the collection has never seen is still forwarded (harmless: it
	// just matches nothing on the Qdrant side).
	require.Equal(t, []string{"text/markdown", "application/pdf"}, q.lastReq.Filter.MimeIn)
	require.Equal(t, 0, q.distinctCalls, "exact-only filters must not pay for a facet round-trip")
}

func TestSearchText_MixedPrefixAndExactDedupes(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes()}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	_, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "x", Filters: &Filters{MimePrefix: []string{"text/plain", "text/"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"text/plain", "text/markdown", "text/markdown+docling/pdf"}, q.lastReq.Filter.MimeIn)
}

func TestSearchText_MimePrefixWithNoKnownMatchShortCircuits(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes(), hits: []QdrantHit{{PointID: "p1", Score: 0.9, Payload: map[string]any{"file_id": "f1"}}}}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	resp, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "x", Filters: &Filters{MimePrefix: []string{"application/"}}, GroupByFile: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Hits, "hits must serialise as [] not null")
	require.Empty(t, resp.Hits)
	require.Empty(t, resp.Files)
	require.Equal(t, 0, q.searchCalls, "an empty mime set can match nothing — skip the vector search")
}

func TestSearchText_MimeFacetUnavailableFallsBackToExactMatch(t *testing.T) {
	q := &fakeQdrant{distinctErr: errors.New("facet down")}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	resp, err := svc.SearchText(context.Background(), SearchRequest{
		Query: "x", Filters: &Filters{MimePrefix: []string{"text/", "text/plain"}},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"text/", "text/plain"}, q.lastReq.Filter.MimeIn, "degrade to the pre-facet behaviour, never fail the search")
	require.Contains(t, resp.Warnings, "mime_facet_unavailable")
}

func TestSearchText_KnownMimesAreCachedAcrossSearches(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes()}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	for i := 0; i < 3; i++ {
		_, err := svc.SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{MimePrefix: []string{"text/"}}})
		require.NoError(t, err)
	}
	require.Equal(t, 1, q.distinctCalls)
}

func TestSearchText_NoMimeFilterNeverFacets(t *testing.T) {
	q := &fakeQdrant{distinct: knownMimes()}
	svc := &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, time.Minute), DefaultTopK: 10}
	_, err := svc.SearchText(context.Background(), SearchRequest{Query: "x"})
	require.NoError(t, err)
	require.Equal(t, 0, q.distinctCalls)
	require.Nil(t, q.lastReq.Filter.MimeIn)
}
