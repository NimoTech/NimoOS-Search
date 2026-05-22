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
