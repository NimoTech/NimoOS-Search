package service

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
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

// ParentChunksResponse is the whole section an anchor chunk belongs to.
type ParentChunksResponse struct {
	FileID        string              `json:"file_id"`
	Kind          string              `json:"kind"`
	ParentID      string              `json:"parent_id,omitempty"`
	Section       string              `json:"section,omitempty"`
	AnchorChunkNo int                 `json:"anchor_chunk_no"`
	Chunks        []ChunkContextChunk `json:"chunks"`
}

// parentMaxChunks bounds a section read; sections are split by Parser at
// target_tokens so real ones are far smaller.
const parentMaxChunks = 50

// GetParentChunks returns every chunk that shares the anchor chunk's
// parent_id (its section), in chunk order — the "read the whole section"
// alternative to a ±window guess. Anchors without a parent_id (pre-0.3.0
// payloads) return just themselves. Same authz semantics as GetFileChunks.
func (s *AuthzService) GetParentChunks(ctx context.Context, fileID, kind string,
	chunkNo int, allowedRoots []string) (*ParentChunksResponse, error) {
	if len(allowedRoots) == 0 {
		return nil, ErrFileNotInScope
	}
	var all []QdrantHit
	offset := ""
	for {
		hits, next, err := s.Qdrant.ScrollByFileID(ctx, collectionTextChunks, fileID, allowedRoots, 500, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, hits...)
		if next == "" || len(hits) == 0 {
			break
		}
		offset = next
	}
	var anchor *QdrantHit
	for i := range all {
		k, _ := all[i].Payload["kind"].(string)
		if k == kind && int(asInt64(all[i].Payload["chunk_no"])) == chunkNo {
			anchor = &all[i]
			break
		}
	}
	if anchor == nil {
		return nil, ErrFileNotInScope
	}
	parentID, _ := anchor.Payload["parent_id"].(string)
	section, _ := anchor.Payload["section"].(string)
	out := []ChunkContextChunk{}
	for _, h := range all {
		k, _ := h.Payload["kind"].(string)
		pid, _ := h.Payload["parent_id"].(string)
		cn := int(asInt64(h.Payload["chunk_no"]))
		if k != kind {
			continue
		}
		if parentID == "" {
			if cn != chunkNo {
				continue
			}
		} else if pid != parentID {
			continue
		}
		out = append(out, chunkContextFromPayload(h.Payload, cn))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ChunkNo < out[j].ChunkNo })
	if len(out) > parentMaxChunks {
		out = out[:parentMaxChunks]
	}
	return &ParentChunksResponse{FileID: fileID, Kind: kind, ParentID: parentID,
		Section: section, AnchorChunkNo: chunkNo, Chunks: out}, nil
}

func chunkContextFromPayload(p map[string]any, cn int) ChunkContextChunk {
	text, _ := p["text"].(string)
	cc := ChunkContextChunk{ChunkNo: cn, Text: text}
	if p["page"] != nil {
		v := int(asInt64(p["page"]))
		cc.Page = &v
	}
	if p["offset_start"] != nil {
		v := asInt64(p["offset_start"])
		cc.OffsetStart = &v
	}
	if p["offset_end"] != nil {
		v := asInt64(p["offset_end"])
		cc.OffsetEnd = &v
	}
	return cc
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

// PhotoCaption looks up the caption text for a single photo (assetID),
// satisfying the CaptionSource interface. file_id follows the convention
// "photos:<asset_id>" (see the NimoOS-Parser write-side convention), and
// reuses ScrollByFileID's root_ids authorization intersection (the same
// semantics as GetFileChunks etc). 5 is plenty - a photo normally has just
// one chunk with kind=="caption", this leaves a bit of headroom in case it
// gets split in the future. No hit (no caption ever generated, or not
// within allowedRoots) returns ("", nil), which is not an error: the caller
// (the aggregate images branch) treats it as fail-open, so there's no need
// here to distinguish "nothing found" from "failure other than a query
// error".
func (s *AuthzService) PhotoCaption(ctx context.Context, assetID string, allowedRoots []string) (string, error) {
	if len(allowedRoots) == 0 {
		return "", nil
	}
	hits, _, err := s.Qdrant.ScrollByFileID(ctx, collectionTextChunks, "photos:"+assetID, allowedRoots, 5, "")
	if err != nil {
		return "", err
	}
	for _, h := range hits {
		kind, _ := h.Payload["kind"].(string)
		if kind != "caption" {
			continue
		}
		text, _ := h.Payload["text"].(string)
		return truncateRunes(text, 200), nil
	}
	return "", nil
}

// truncateRunes truncates s to at most max runes (not bytes), appending an
// ellipsis "…" when it overflows. We truncate by rune because captions
// often contain multi-byte characters (CJK/emoji); cutting by byte would
// split a character and produce garbled output.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

type DocumentTextResponse struct {
	FileID     string `json:"file_id"`
	Text       string `json:"text"`
	Truncated  bool   `json:"truncated"`
	TotalChars int    `json:"total_chars"`
	NextOffset int    `json:"next_offset"`
}

// GetDocumentText reconstructs a document's full body text from its indexed
// chunks (kind=="body" only), then returns the character window
// [charOffset, charOffset+maxChars). Chunks are stitched in chunk_no order
// using their character offsets so overlapping chunks (chunk_plain adds ~320
// chars of overlap) don't duplicate text, while non-overlapping chunks
// (chunk_markdown/source) concatenate cleanly. A "\n\n[Page N]\n\n" marker is
// inserted wherever the page changes. All slicing is rune-based because Parser
// offsets are character counts — byte slicing would corrupt multibyte text.
// Same root authz as GetFileChunks: empty allowedRoots or no matching chunk →
// ErrFileNotInScope (route maps to 404). Tombstoned chunks have empty root_ids
// and are already excluded by the RootIDsAny filter inside ScrollByFileID.
func (s *AuthzService) GetDocumentText(ctx context.Context, fileID string,
	allowedRoots []string, charOffset, maxChars int) (*DocumentTextResponse, error) {
	if len(allowedRoots) == 0 {
		return nil, ErrFileNotInScope
	}
	body := []FileChunk{}
	off := ""
	for {
		hits, next, err := s.Qdrant.ScrollByFileID(ctx, collectionTextChunks, fileID, allowedRoots, 500, off)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			fc := hitToFileChunk(h)
			if fc.Kind == "body" {
				body = append(body, fc)
			}
		}
		if next == "" {
			break
		}
		off = next
	}
	if len(body) == 0 {
		return nil, ErrFileNotInScope
	}
	sort.SliceStable(body, func(i, j int) bool { return body[i].ChunkNo < body[j].ChunkNo })

	var sb strings.Builder
	var cursor int64 // characters consumed from the original document text
	var lastPage *int
	for _, c := range body {
		if c.Page != nil && (lastPage == nil || *c.Page != *lastPage) {
			sb.WriteString("\n\n[Page ")
			sb.WriteString(strconv.Itoa(*c.Page))
			sb.WriteString("]\n\n")
			p := *c.Page
			lastPage = &p
		}
		if c.OffsetStart != nil && c.OffsetEnd != nil {
			if *c.OffsetStart >= cursor {
				sb.WriteString(c.Text)
			} else {
				skip := cursor - *c.OffsetStart
				runes := []rune(c.Text)
				if skip < int64(len(runes)) {
					sb.WriteString(string(runes[skip:]))
				}
			}
			if *c.OffsetEnd > cursor {
				cursor = *c.OffsetEnd
			}
		} else {
			// No offsets recorded: fall back to plain concatenation.
			sb.WriteString(c.Text)
			sb.WriteString("\n")
		}
	}

	full := []rune(sb.String())
	total := len(full)
	start := charOffset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + maxChars
	if maxChars <= 0 || end > total {
		end = total
	}
	truncated := end < total
	next := 0
	if truncated {
		next = end
	}
	return &DocumentTextResponse{
		FileID: fileID, Text: string(full[start:end]),
		Truncated: truncated, TotalChars: total, NextOffset: next,
	}, nil
}
