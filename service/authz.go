package service

import (
	"context"
	"errors"
	"sort"
)

var ErrFileNotInScope = errors.New("file not in user's accessible roots (or not indexed)")

// collectionTextChunks is the Qdrant collection holding all text-modality chunks
// (body / ocr / caption / transcript / summary). Centralized here so a rename
// stays a one-line change.
const collectionTextChunks = "text_chunks"

type AuthzService struct {
	Qdrant QdrantAPI
}

type FileChunk struct {
	Kind         string `json:"kind"`
	ChunkNo      int    `json:"chunk_no"`
	Text         string `json:"text"`
	Page         *int   `json:"page,omitempty"`
	OffsetStart  *int64 `json:"offset_start,omitempty"`
	OffsetEnd    *int64 `json:"offset_end,omitempty"`
	FrameMsStart *int64 `json:"frame_ms_start,omitempty"`
	FrameMsEnd   *int64 `json:"frame_ms_end,omitempty"`
}

type FileChunksResponse struct {
	FileID string      `json:"file_id"`
	Chunks []FileChunk `json:"chunks"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
	Total  int         `json:"total"`
}

// GetFileChunks lists chunks for fileID, BUT only those whose payload.root_ids
// intersect with allowedRoots. Returns ErrFileNotInScope if nothing matches —
// route handler maps to 404 (per spec §4.5: don't return 403 to avoid leaking
// existence of file_ids).
func (s *AuthzService) GetFileChunks(ctx context.Context, fileID string,
	allowedRoots []string, offset, limit int) (*FileChunksResponse, error) {
	if len(allowedRoots) == 0 {
		return nil, ErrFileNotInScope
	}
	all := []FileChunk{}
	off := ""
	for {
		hits, next, err := s.Qdrant.ScrollByFileID(ctx, collectionTextChunks, fileID, allowedRoots, 500, off)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			all = append(all, hitToFileChunk(h))
		}
		if next == "" {
			break
		}
		off = next
	}
	if len(all) == 0 {
		return nil, ErrFileNotInScope
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].ChunkNo < all[j].ChunkNo
	})
	total := len(all)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return &FileChunksResponse{
		FileID: fileID, Chunks: all[start:end], Offset: offset, Limit: limit, Total: total,
	}, nil
}

type ChunkContextChunk struct {
	ChunkNo     int    `json:"chunk_no"`
	Text        string `json:"text"`
	Page        *int   `json:"page,omitempty"`
	OffsetStart *int64 `json:"offset_start,omitempty"`
	OffsetEnd   *int64 `json:"offset_end,omitempty"`
}

type ChunkContextResponse struct {
	FileID        string              `json:"file_id"`
	Kind          string              `json:"kind"`
	AnchorChunkNo int                 `json:"anchor_chunk_no"`
	Chunks        []ChunkContextChunk `json:"chunks"`
}

// GetChunkWindow returns chunks in [chunk_no - window, chunk_no + window]
// for the same (file_id, kind). Same authz semantics as GetFileChunks.
func (s *AuthzService) GetChunkWindow(ctx context.Context, fileID, kind string,
	chunkNo, window int, allowedRoots []string) (*ChunkContextResponse, error) {
	if len(allowedRoots) == 0 {
		return nil, ErrFileNotInScope
	}
	hits, _, err := s.Qdrant.ScrollByFileID(ctx, collectionTextChunks, fileID, allowedRoots, 1000, "")
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, ErrFileNotInScope
	}
	out := []ChunkContextChunk{}
	for _, h := range hits {
		k, _ := h.Payload["kind"].(string)
		if k != kind {
			continue
		}
		cn := int(asInt64(h.Payload["chunk_no"]))
		if cn < chunkNo-window || cn > chunkNo+window {
			continue
		}
		text, _ := h.Payload["text"].(string)
		cc := ChunkContextChunk{ChunkNo: cn, Text: text}
		if h.Payload["page"] != nil {
			v := int(asInt64(h.Payload["page"]))
			cc.Page = &v
		}
		if h.Payload["offset_start"] != nil {
			v := asInt64(h.Payload["offset_start"])
			cc.OffsetStart = &v
		}
		if h.Payload["offset_end"] != nil {
			v := asInt64(h.Payload["offset_end"])
			cc.OffsetEnd = &v
		}
		out = append(out, cc)
	}
	if len(out) == 0 {
		return nil, ErrFileNotInScope
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ChunkNo < out[j].ChunkNo })
	return &ChunkContextResponse{
		FileID: fileID, Kind: kind, AnchorChunkNo: chunkNo, Chunks: out,
	}, nil
}

func hitToFileChunk(h QdrantHit) FileChunk {
	kind, _ := h.Payload["kind"].(string)
	text, _ := h.Payload["text"].(string)
	cn := int(asInt64(h.Payload["chunk_no"]))
	fc := FileChunk{Kind: kind, ChunkNo: cn, Text: text}
	if h.Payload["page"] != nil {
		v := int(asInt64(h.Payload["page"]))
		fc.Page = &v
	}
	if h.Payload["offset_start"] != nil {
		v := asInt64(h.Payload["offset_start"])
		fc.OffsetStart = &v
	}
	if h.Payload["offset_end"] != nil {
		v := asInt64(h.Payload["offset_end"])
		fc.OffsetEnd = &v
	}
	if h.Payload["frame_ms_start"] != nil {
		v := asInt64(h.Payload["frame_ms_start"])
		fc.FrameMsStart = &v
	}
	if h.Payload["frame_ms_end"] != nil {
		v := asInt64(h.Payload["frame_ms_end"])
		fc.FrameMsEnd = &v
	}
	return fc
}
