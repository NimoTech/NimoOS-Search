package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func sectionHit(id, parent, section string, chunkNo int, text string, start, end int64, score float32) QdrantHit {
	return QdrantHit{PointID: id, Score: score, Payload: map[string]any{
		"file_id": "f", "kind": "body", "chunk_no": int64(chunkNo), "text": text,
		"parent_id": parent, "section": section, "section_no": int64(1),
		"offset_start": start, "offset_end": end,
	}}
}

func mergeService(q *fakeQdrant) *SearchService {
	return &SearchService{Parser: &fakeParser{}, Qdrant: q, Cache: NewEmbedCache(10, 0), DefaultTopK: 10, RerankerCandidates: 8, MaxTopK: 100}
}

// Small chunks give recall precision; the answer wants the section. Chunks
// of one section that both rank are folded into a single section-level hit
// at the rank of the best one, text in document order, offsets spanning.
func TestSearchText_MergesChunksOfSameSection(t *testing.T) {
	q := &fakeQdrant{hits: []QdrantHit{
		sectionHit("p1", "A", "Guide > Setup", 0, "t0", 0, 2, 0.9),
		sectionHit("p2", "B", "Other", 5, "t5", 50, 52, 0.8),
		sectionHit("p3", "A", "Guide > Setup", 1, "t1", 3, 5, 0.7),
	}}
	resp, err := mergeService(q).SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 2)
	a := resp.Hits[0]
	require.Equal(t, "A", a.ParentID)
	require.Equal(t, "Guide > Setup", a.Section)
	require.Equal(t, 2, a.MergedChunks)
	require.Equal(t, "t0\nt1", *a.Preview.Text)
	require.Equal(t, int64(0), *a.Cite.OffsetStart)
	require.Equal(t, int64(5), *a.Cite.OffsetEnd)
	require.Equal(t, 0, a.Cite.ChunkNo, "cite points at the section's first chunk")
	require.InDelta(t, 0.9, a.Score, 1e-6, "section keeps its best chunk's score")
	require.Equal(t, "B", resp.Hits[1].ParentID)
	require.Equal(t, 1, resp.Hits[1].MergedChunks)
}

func TestSearchText_ParentMergeKeepsRankOfBestChunk(t *testing.T) {
	q := &fakeQdrant{hits: []QdrantHit{
		sectionHit("p2", "B", "Other", 5, "t5", 50, 52, 0.9),
		sectionHit("p1", "A", "S", 0, "t0", 0, 2, 0.8),
		sectionHit("p3", "A", "S", 1, "t1", 3, 5, 0.7),
	}}
	resp, err := mergeService(q).SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Equal(t, []string{"B", "A"}, []string{resp.Hits[0].ParentID, resp.Hits[1].ParentID})
}

func TestSearchText_ParentMergeCapsTextButStillDedupes(t *testing.T) {
	q := &fakeQdrant{hits: []QdrantHit{
		sectionHit("p1", "A", "S", 0, "0123456789", 0, 10, 0.9),
		sectionHit("p3", "A", "S", 1, "abcdefghij", 11, 21, 0.7),
	}}
	s := mergeService(q)
	s.ParentMergeMaxChars = 12
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 1, "the sibling is still folded away even when its text does not fit")
	require.Equal(t, "0123456789", *resp.Hits[0].Preview.Text)
	require.Equal(t, 2, resp.Hits[0].MergedChunks)
}

func TestSearchText_ParentMergeDisabledKeepsChunks(t *testing.T) {
	q := &fakeQdrant{hits: []QdrantHit{
		sectionHit("p1", "A", "S", 0, "t0", 0, 2, 0.9),
		sectionHit("p3", "A", "S", 1, "t1", 3, 5, 0.7),
	}}
	s := mergeService(q)
	s.ParentMergeDisabled = true
	resp, err := s.SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 2)
}

func TestSearchText_ChunksWithoutParentAreNeverMerged(t *testing.T) {
	// Pre-0.3.0 payloads have no parent_id; two such chunks of one file must stay separate.
	q := &fakeQdrant{hits: []QdrantHit{
		{PointID: "p1", Score: 0.9, Payload: map[string]any{"file_id": "f", "kind": "body", "chunk_no": int64(0), "text": "a"}},
		{PointID: "p2", Score: 0.8, Payload: map[string]any{"file_id": "f", "kind": "body", "chunk_no": int64(1), "text": "b"}},
	}}
	resp, err := mergeService(q).SearchText(context.Background(), SearchRequest{Query: "x", Filters: &Filters{RootIDs: []string{"r1"}}})
	require.NoError(t, err)
	require.Len(t, resp.Hits, 2)
}
