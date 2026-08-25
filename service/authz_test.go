package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetFileChunks_FiltersByAllowedRoots(t *testing.T) {
	q := &recordingQdrant{
		hits: []QdrantHit{
			{PointID: "p1", Payload: map[string]any{
				"file_id": "f1", "kind": "body", "chunk_no": int64(0),
				"text": "alpha", "root_ids": []any{"r1"},
			}},
		},
	}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetFileChunks(context.Background(), "f1", []string{"r1"}, 0, 50)
	require.NoError(t, err)
	require.Len(t, out.Chunks, 1)
	require.Equal(t, []string{"r1"}, q.lastFilter.RootIDsAny)
	require.Equal(t, []string{"f1"}, q.lastFilter.FileIDsAny)
}

func TestGetFileChunks_NoMatchReturnsErrNotInScope(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{}}
	svc := &AuthzService{Qdrant: q}
	_, err := svc.GetFileChunks(context.Background(), "f-secret", []string{"r1"}, 0, 50)
	require.ErrorIs(t, err, ErrFileNotInScope)
}

func TestGetChunkWindow_ReturnsNeighbors(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"chunk_no": int64(5), "text": "five", "kind": "body"}},
		{Payload: map[string]any{"chunk_no": int64(6), "text": "six", "kind": "body"}},
		{Payload: map[string]any{"chunk_no": int64(7), "text": "seven (anchor)", "kind": "body"}},
		{Payload: map[string]any{"chunk_no": int64(8), "text": "eight", "kind": "body"}},
		{Payload: map[string]any{"chunk_no": int64(9), "text": "nine", "kind": "body"}},
	}}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetChunkWindow(context.Background(), "f1", "body", 7, 2, []string{"r1"})
	require.NoError(t, err)
	require.Equal(t, 7, out.AnchorChunkNo)
	require.Equal(t, []int{5, 6, 7, 8, 9}, chunkNos(out.Chunks))
}

func chunkNos(cs []ChunkContextChunk) []int {
	out := make([]int, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ChunkNo)
	}
	return out
}

// recordingQdrant captures the last filter passed in
type recordingQdrant struct {
	hits       []QdrantHit
	lastFilter *QdrantFilter
}

func (r *recordingQdrant) SearchTextHybrid(ctx context.Context, q QdrantSearchRequest) ([]QdrantHit, error) {
	return nil, errors.New("not used")
}
func (r *recordingQdrant) ScrollByFileID(ctx context.Context, c, fid string, roots []string, lim int, off string) ([]QdrantHit, string, error) {
	r.lastFilter = &QdrantFilter{FileIDsAny: []string{fid}, RootIDsAny: roots}
	return r.hits, "", nil
}
func (r *recordingQdrant) Count(ctx context.Context, c string) (uint64, error) { return 0, nil }
func (r *recordingQdrant) DistinctValues(ctx context.Context, c, k string) ([]string, error) {
	return nil, nil
}

func TestGetDocumentText_StitchesBodyChunksWithPageMarkers(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "alpha", "page": int64(1), "offset_start": int64(0), "offset_end": int64(5), "root_ids": []any{"r1"}}},
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1),
			"text": "betaX", "page": int64(2), "offset_start": int64(5), "offset_end": int64(10), "root_ids": []any{"r1"}}},
	}}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, 0, 24000)
	require.NoError(t, err)
	require.Equal(t, "\n\n[Page 1]\n\nalpha\n\n[Page 2]\n\nbetaX", out.Text)
	require.False(t, out.Truncated)
}

func TestGetDocumentText_DedupsOverlapByOffset(t *testing.T) {
	// chunk_plain-style overlap: chunk1 [0,6)="abcdef", chunk2 [4,10)="efghij"
	// (offsets are CHARACTER counts; the 2-char overlap "ef" must not duplicate).
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "abcdef", "offset_start": int64(0), "offset_end": int64(6), "root_ids": []any{"r1"}}},
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1),
			"text": "efghij", "offset_start": int64(4), "offset_end": int64(10), "root_ids": []any{"r1"}}},
	}}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, 0, 24000)
	require.NoError(t, err)
	require.Equal(t, "abcdefghij", out.Text)
}

func TestGetDocumentText_RuneSafeWindow(t *testing.T) {
	// 4 CJK chars; window of 2 chars must not split a multibyte rune.
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "你好世界", "offset_start": int64(0), "offset_end": int64(4), "root_ids": []any{"r1"}}},
	}}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, 0, 2)
	require.NoError(t, err)
	require.Equal(t, "你好", out.Text)
	require.True(t, out.Truncated)
	require.Equal(t, 2, out.NextOffset)
	require.Equal(t, 4, out.TotalChars)
}

func TestGetDocumentText_IgnoresNonBody(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "real", "offset_start": int64(0), "offset_end": int64(4), "root_ids": []any{"r1"}}},
		{Payload: map[string]any{"file_id": "f1", "kind": "ocr", "chunk_no": int64(0),
			"text": "noise", "offset_start": int64(0), "offset_end": int64(5), "root_ids": []any{"r1"}}},
	}}
	svc := &AuthzService{Qdrant: q}
	out, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, 0, 24000)
	require.NoError(t, err)
	require.Equal(t, "real", out.Text)
}

func TestGetDocumentText_NoMatchReturnsErrNotInScope(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{}}
	svc := &AuthzService{Qdrant: q}
	_, err := svc.GetDocumentText(context.Background(), "f-secret", []string{"r1"}, 0, 24000)
	require.ErrorIs(t, err, ErrFileNotInScope)
}

func TestGetDocumentText_PagingRoundTrip(t *testing.T) {
	// One 10-char body chunk. Page through it in 4-char windows using the
	// returned NextOffset and confirm the windows reconstruct the whole
	// document with no loss or overlap (the headline long-doc paging path).
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "abcdefghij", "offset_start": int64(0), "offset_end": int64(10), "root_ids": []any{"r1"}}},
	}}
	svc := &AuthzService{Qdrant: q}

	p1, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, 0, 4)
	require.NoError(t, err)
	require.Equal(t, "abcd", p1.Text)
	require.True(t, p1.Truncated)
	require.Equal(t, 4, p1.NextOffset)
	require.Equal(t, 10, p1.TotalChars)

	p2, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, p1.NextOffset, 4)
	require.NoError(t, err)
	require.Equal(t, "efgh", p2.Text)
	require.True(t, p2.Truncated)
	require.Equal(t, 8, p2.NextOffset)

	p3, err := svc.GetDocumentText(context.Background(), "f1", []string{"r1"}, p2.NextOffset, 4)
	require.NoError(t, err)
	require.Equal(t, "ij", p3.Text)
	require.False(t, p3.Truncated)
	require.Equal(t, 0, p3.NextOffset)

	require.Equal(t, "abcdefghij", p1.Text+p2.Text+p3.Text)
}
