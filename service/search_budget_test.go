package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func budgetService(p *fakeParser, q *fakeQdrant, rerankBudget, maxTopK int) *SearchService {
	return &SearchService{
		Parser: p, Qdrant: q, Cache: NewEmbedCache(10, 0),
		DefaultTopK: 20, RerankerCandidates: rerankBudget, MaxTopK: maxTopK,
	}
}

func chunkHits(n int) []QdrantHit {
	out := make([]QdrantHit, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, QdrantHit{PointID: "p" + string(rune('0'+i)), Score: float32(1.0) - float32(i)*0.1,
			Payload: map[string]any{"file_id": "f", "kind": "body", "chunk_no": int64(i), "text": "t"}})
	}
	return out
}

func ids(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.PointID)
	}
	return out
}

// The reranker costs ~1.3 s per candidate. RerankerCandidates is that
// budget; it must not be silently raised to top_k (20 by default), which put
// every default query past ParserTimeoutSec and into rerank_unavailable.
func TestSearchText_RerankBudgetIsIndependentOfTopK(t *testing.T) {
	p := &fakeParser{rerankScores: []RerankScore{{ID: "p2", Score: 0.9}, {ID: "p1", Score: 0.1}}}
	q := &fakeQdrant{hits: chunkHits(5)}
	s := budgetService(p, q, 2, 100)
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", TopK: 5, Rerank: true, Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Len(t, p.rerankCands, 2, "only the budgeted top candidates go to the reranker")
	require.Equal(t, 5, q.lastReq.Limit, "vector search still fetches top_k candidates")
	// reranked hits first (by rerank score), the rest keep vector order behind them
	require.Equal(t, []string{"p2", "p1", "p3", "p4", "p5"}, ids(resp.Hits))
}

// Cross-encoder scores and cosine scores are not comparable; a partial
// rerank response must never interleave the two scales in one sort.
func TestSearchText_PartialRerankKeepsUnrankedBehindRanked(t *testing.T) {
	p := &fakeParser{rerankScores: []RerankScore{{ID: "p3", Score: 0.9}, {ID: "p1", Score: 0.2}}} // p2 missing
	q := &fakeQdrant{hits: chunkHits(5)}
	s := budgetService(p, q, 3, 100)
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", TopK: 5, Rerank: true, Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"p3", "p1", "p2", "p4", "p5"}, ids(resp.Hits))
}

func TestSearchText_TopKClampedToMaxTopK(t *testing.T) {
	p := &fakeParser{}
	q := &fakeQdrant{hits: chunkHits(5)}
	s := budgetService(p, q, 2, 3)
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", TopK: 1000, Rerank: false, Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Equal(t, 3, q.lastReq.Limit, "a client cannot turn top_k into an unbounded Qdrant limit")
	require.LessOrEqual(t, len(resp.Hits), 3)
}

func TestSearchText_RerankDisabledByOperatorSkipsParser(t *testing.T) {
	p := &fakeParser{rerankScores: []RerankScore{{ID: "p1", Score: 0.9}}}
	q := &fakeQdrant{hits: chunkHits(2)}
	s := budgetService(p, q, 8, 100)
	s.RerankerDisabled = true
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", TopK: 2, Rerank: true, Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Equal(t, 0, p.rerankCalls)
	require.Contains(t, resp.Warnings, "rerank_disabled")
}
