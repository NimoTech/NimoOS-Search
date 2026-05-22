package service

import (
	"context"
	"sort"
	"time"
)

// SearchRequest mirrors the JSON shape from OpenAPI components.SearchRequest.
type SearchRequest struct {
	Query   string   `json:"query"`
	Filters *Filters `json:"filters,omitempty"`
	TopK    int      `json:"top_k,omitempty"`
	Rerank  bool     `json:"rerank,omitempty"`
}

type Hit struct {
	Score        float64        `json:"score"`
	RawScore     float64        `json:"raw_score"`
	Collection   string         `json:"collection"`
	FileID       string         `json:"file_id"`
	Paths        []FilePath     `json:"paths"`
	Mime         string         `json:"mime"`
	Kind         string         `json:"kind"`
	Cite         Cite           `json:"cite"`
	Preview      Preview        `json:"preview"`
	PayloadExtra map[string]any `json:"payload_extra"`
	PointID      string         `json:"-"` // internal, used for rerank pairing
}

type Cite struct {
	Page         *int   `json:"page"`
	OffsetStart  *int64 `json:"offset_start"`
	OffsetEnd    *int64 `json:"offset_end"`
	FrameMsStart *int64 `json:"frame_ms_start"`
	FrameMsEnd   *int64 `json:"frame_ms_end"`
	ChunkNo      int    `json:"chunk_no"`
}

type Preview struct {
	Text         *string `json:"text"`
	ThumbnailURL *string `json:"thumbnail_url"`
}

type SearchStats struct {
	TotalCandidates int `json:"total_candidates"`
	RerankMs        int `json:"rerank_ms"`
	EmbedMs         int `json:"embed_ms"`
	VectorSearchMs  int `json:"vector_search_ms"`
	ExpandMs        int `json:"expand_ms"`
}

type SearchResponse struct {
	Hits     []Hit       `json:"hits"`
	Stats    SearchStats `json:"stats"`
	Warnings []string    `json:"warnings"`
}

// ParserAPI / QdrantAPI are interfaces so tests can swap fakes in.
type ParserAPI interface {
	Embed(ctx context.Context, model, inputType, text, imageB64 string) (*EmbedResult, error)
	Rerank(ctx context.Context, q string, c []RerankCandidate, topK *int) (*RerankResult, error)
	ExpandFiles(ctx context.Context, fileIDs []string) (*ExpandFilesResult, error)
}

type QdrantAPI interface {
	SearchTextHybrid(ctx context.Context, r QdrantSearchRequest) ([]QdrantHit, error)
	ScrollByFileID(ctx context.Context, collection, fileID string, allowedRoots []string, limit int, offset string) ([]QdrantHit, string, error)
	Count(ctx context.Context, collection string) (uint64, error)
}

type SearchService struct {
	Parser             ParserAPI
	Qdrant             QdrantAPI
	Cache              *EmbedCache
	ParserVersion      string
	DefaultTopK        int
	RerankerCandidates int
}

// SearchText is the full /v1/search/text orchestration:
//
//	embed → qdrant text_chunks hybrid search → rerank (with fallback) → expand paths.
//
// The caller (route handler) is responsible for ApplyScope + 200/503 mapping.
func (s *SearchService) SearchText(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	topK := req.TopK
	if topK <= 0 {
		topK = s.DefaultTopK
	}
	candidates := s.RerankerCandidates
	if candidates < topK {
		candidates = topK
	}
	if req.Filters == nil {
		req.Filters = &Filters{}
	}
	warnings := []string{}
	stats := SearchStats{}

	// 1. Embed (with cache + singleflight)
	t := time.Now()
	emb, err := s.Cache.GetOrLoad(ctx, HashQuery(req.Query),
		func(ctx context.Context) (*EmbedResult, error) {
			return s.Parser.Embed(ctx, "bge-m3", "text", req.Query, "")
		})
	stats.EmbedMs = int(time.Since(t).Milliseconds())
	if err != nil {
		return nil, err
	}

	// 2. Qdrant search
	t = time.Now()
	qhits, err := s.Qdrant.SearchTextHybrid(ctx, QdrantSearchRequest{
		Collection: "text_chunks",
		Dense:      emb.Dense,
		Sparse:     emb.Sparse,
		Filter: &QdrantFilter{
			RootIDsAny: req.Filters.RootIDs,
			MimePrefix: req.Filters.MimePrefix,
			KindIn:     req.Filters.KindIn,
			LangIn:     req.Filters.LangIn,
		},
		Limit: candidates,
	})
	stats.VectorSearchMs = int(time.Since(t).Milliseconds())
	if err != nil {
		return nil, err
	}
	stats.TotalCandidates = len(qhits)

	// Build initial hits from Qdrant (raw_score = vector score)
	hits := make([]Hit, 0, len(qhits))
	for _, qh := range qhits {
		hits = append(hits, buildHitFromPayload(qh))
	}

	// 3. Rerank (with fallback)
	if req.Rerank && len(hits) > 0 {
		t = time.Now()
		cands := make([]RerankCandidate, 0, len(hits))
		for _, h := range hits {
			text := ""
			if h.Preview.Text != nil {
				text = *h.Preview.Text
			}
			cands = append(cands, RerankCandidate{ID: h.PointID, Text: text})
		}
		rr, err := s.Parser.Rerank(ctx, req.Query, cands, nil)
		stats.RerankMs = int(time.Since(t).Milliseconds())
		if err != nil {
			warnings = append(warnings, "rerank_unavailable")
			// keep score = raw_score for all hits
		} else {
			scoreByID := make(map[string]float64, len(rr.Scores))
			for _, s := range rr.Scores {
				scoreByID[s.ID] = s.Score
			}
			for i := range hits {
				if sc, ok := scoreByID[hits[i].PointID]; ok {
					hits[i].Score = sc
				}
			}
		}
	}

	// 4. Sort by Score desc and truncate to top_k
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}

	// 5. Expand paths via Parser
	t = time.Now()
	if len(hits) > 0 {
		idSet := make(map[string]struct{})
		ids := []string{}
		for _, h := range hits {
			if _, ok := idSet[h.FileID]; !ok {
				idSet[h.FileID] = struct{}{}
				ids = append(ids, h.FileID)
			}
		}
		exp, err := s.Parser.ExpandFiles(ctx, ids)
		stats.ExpandMs = int(time.Since(t).Milliseconds())
		if err != nil {
			warnings = append(warnings, "path_expand_unavailable")
		} else {
			byID := make(map[string]FileRecord, len(exp.Files))
			for _, f := range exp.Files {
				byID[f.FileID] = f
			}
			for i := range hits {
				if rec, ok := byID[hits[i].FileID]; ok {
					hits[i].Paths = rec.Paths
					if hits[i].Mime == "" {
						hits[i].Mime = rec.Mime
					}
				}
			}
		}
	}

	return &SearchResponse{Hits: hits, Stats: stats, Warnings: warnings}, nil
}

// buildHitFromPayload turns a Qdrant hit into a Search Hit with sensible
// defaults. Payload fields that don't exist for MVP (e.g. page for non-PDF)
// stay nil.
func buildHitFromPayload(qh QdrantHit) Hit {
	p := qh.Payload
	text := stringOrNilFromAny(p["text"])
	kind, _ := p["kind"].(string)
	mime, _ := p["mime"].(string)
	fileID, _ := p["file_id"].(string)
	chunkNo := int(asInt64(p["chunk_no"]))
	cite := Cite{ChunkNo: chunkNo}
	if p["page"] != nil {
		v := int(asInt64(p["page"]))
		cite.Page = &v
	}
	if p["offset_start"] != nil {
		v := asInt64(p["offset_start"])
		cite.OffsetStart = &v
	}
	if p["offset_end"] != nil {
		v := asInt64(p["offset_end"])
		cite.OffsetEnd = &v
	}
	if p["frame_ms_start"] != nil {
		v := asInt64(p["frame_ms_start"])
		cite.FrameMsStart = &v
	}
	if p["frame_ms_end"] != nil {
		v := asInt64(p["frame_ms_end"])
		cite.FrameMsEnd = &v
	}
	return Hit{
		RawScore:     float64(qh.Score),
		Score:        float64(qh.Score),
		Collection:   "text_chunks",
		FileID:       fileID,
		Mime:         mime,
		Kind:         kind,
		Cite:         cite,
		Preview:      Preview{Text: text},
		PayloadExtra: map[string]any{},
		PointID:      qh.PointID,
	}
}

func stringOrNilFromAny(v any) *string {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
