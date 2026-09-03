package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func parentChunk(chunkNo int, parent, text string) QdrantHit {
	return QdrantHit{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(chunkNo),
		"text": text, "parent_id": parent, "section": "S-" + parent, "root_ids": []any{"r1"}}}
}

// read_file_chunk(parent=true): return the whole section the anchor chunk
// belongs to, in document order, instead of a ±window guess.
func TestGetParentChunks_ReturnsWholeSectionInOrder(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		parentChunk(3, "A", "a3"), parentChunk(0, "B", "b0"), parentChunk(1, "A", "a1"), parentChunk(2, "A", "a2"), parentChunk(9, "C", "c9"),
	}}
	s := &AuthzService{Qdrant: q}
	out, err := s.GetParentChunks(context.Background(), "f1", "body", 2, []string{"r1"})
	require.NoError(t, err)
	require.Equal(t, "A", out.ParentID)
	require.Equal(t, "S-A", out.Section)
	require.Equal(t, 2, out.AnchorChunkNo)
	require.Equal(t, []int{1, 2, 3}, []int{out.Chunks[0].ChunkNo, out.Chunks[1].ChunkNo, out.Chunks[2].ChunkNo})
}

func TestGetParentChunks_AnchorWithoutParentFallsBackToItself(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0), "text": "legacy", "root_ids": []any{"r1"}}},
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1), "text": "legacy2", "root_ids": []any{"r1"}}},
	}}
	s := &AuthzService{Qdrant: q}
	out, err := s.GetParentChunks(context.Background(), "f1", "body", 1, []string{"r1"})
	require.NoError(t, err)
	require.Len(t, out.Chunks, 1)
	require.Equal(t, 1, out.Chunks[0].ChunkNo)
}

func TestGetParentChunks_NoRootsIsOutOfScope(t *testing.T) {
	s := &AuthzService{Qdrant: &recordingQdrant{}}
	_, err := s.GetParentChunks(context.Background(), "f1", "body", 1, nil)
	require.ErrorIs(t, err, ErrFileNotInScope)
}
