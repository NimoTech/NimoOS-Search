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

type fakeQdrant struct{ hits []QdrantHit }

func (f *fakeQdrant) SearchTextHybrid(ctx context.Context, r QdrantSearchRequest) ([]QdrantHit, error) {
	return f.hits, nil
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

func (e *errParser) Embed(ctx context.Context, m, t, x, b string) (*EmbedResult, error) { return nil, e.err }
func (e *errParser) Rerank(ctx context.Context, q string, c []RerankCandidate, k *int) (*RerankResult, error) {
	return nil, errors.New("not called")
}
func (e *errParser) ExpandFiles(ctx context.Context, ids []string) (*ExpandFilesResult, error) {
	return nil, errors.New("not called")
}
