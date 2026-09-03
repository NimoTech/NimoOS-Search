package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// SearchRequest mirrors the JSON shape from OpenAPI components.SearchRequest.
type SearchRequest struct {
	Query   string   `json:"query"`
	Filters *Filters `json:"filters,omitempty"`
	TopK    int      `json:"top_k,omitempty"`
	Rerank  bool     `json:"rerank,omitempty"`
	// GroupByFile collapses chunk hits into file-level results: top_k then means
	// "top K files", each carrying up to MaxChunksPerFile chunks (best first).
	GroupByFile      bool `json:"group_by_file,omitempty"`
	MaxChunksPerFile int  `json:"max_chunks_per_file,omitempty"`
	// UserID is the caller (from X-NimoOS-User-ID), forwarded to Photos when
	// resolving photos:<asset_id> hits. Not part of the request body.
	UserID string `json:"-"`
}

// FileGroup is one file-level result produced when GroupByFile is set.
// Paths/Mime/Kind/Score are taken from the file's best-scoring chunk.
type FileGroup struct {
	FileID string     `json:"file_id"`
	Paths  []FilePath `json:"paths"`
	Mime   string     `json:"mime"`
	Kind   string     `json:"kind"`
	Score  float64    `json:"score"`
	Chunks []Hit      `json:"chunks"`
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
	Files    []FileGroup `json:"files,omitempty"`
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
	// DistinctValues lists every distinct value of a keyword payload field
	// (Qdrant facet). Used to expand mime_prefix into exact mimes.
	DistinctValues(ctx context.Context, collection, key string) ([]string, error)
}

type SearchService struct {
	Parser ParserAPI
	Qdrant QdrantAPI
	// Photos resolves photos:<asset_id> hits to a file path (see
	// expandPhotoPaths). nil disables the lookup; such hits keep paths=null.
	Photos        PhotoAssetLookup
	Cache         *EmbedCache
	ParserVersion string
	DefaultTopK   int
	// MaxTopK caps a request's top_k (and therefore the Qdrant limit, the
	// rerank input and the ExpandFiles id list). <= 0 means no cap.
	MaxTopK int
	// RerankerCandidates is the cross-encoder budget per query: how many of
	// the top vector hits are reranked. It is independent of top_k — at
	// ~1.3 s per candidate, letting top_k (20 by default) raise it silently
	// pushed every default query past ParserTimeoutSec. <= 0 reranks all.
	RerankerCandidates int
	// RerankerDisabled is the operator kill-switch (config RerankerEnabled =
	// false). Zero value keeps reranking on.
	RerankerDisabled bool

	// mimeCache memoises the facet over payload.mime that mime_prefix
	// expansion needs; see knownMimes.
	mimeCache mimeFacetCache
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
	if s.MaxTopK > 0 && topK > s.MaxTopK {
		topK = s.MaxTopK
	}
	maxChunks := req.MaxChunksPerFile
	if maxChunks <= 0 {
		maxChunks = 5
	}
	if maxChunks > 20 {
		maxChunks = 20
	}
	// candidates is the vector-search limit: enough to answer top_k, and at
	// least the rerank budget so the reranker sees its full input. The rerank
	// budget itself never grows with top_k (see RerankerCandidates).
	candidates := topK
	if s.RerankerCandidates > candidates {
		candidates = s.RerankerCandidates
	}
	// Grouping by file needs enough chunk candidates to cover top_k files.
	// Bound our own expansion at 100.
	if req.GroupByFile {
		want := topK * maxChunks
		if want > 100 {
			want = 100
		}
		if want > candidates {
			candidates = want
		}
	}
	if req.Filters == nil {
		req.Filters = &Filters{}
	}
	warnings := []string{}
	stats := SearchStats{}

	// 0. Resolve mime_prefix into exact mime values (Qdrant has no prefix
	//    match). Prefixes are entries ending in "/"; exact mimes pass through.
	//    A prefix that matches nothing in the collection can't produce hits,
	//    so short-circuit before paying for the embedding + vector search.
	mimeIn := req.Filters.MimePrefix
	if hasMimePrefixEntries(mimeIn) {
		known, err := s.knownMimes(ctx)
		if err != nil {
			// Degrade to the pre-facet behaviour (exact match on the raw
			// list) rather than failing the whole search.
			warnings = append(warnings, "mime_facet_unavailable")
		} else {
			mimeIn = expandMimePrefixes(mimeIn, known)
			if len(mimeIn) == 0 {
				return &SearchResponse{Hits: []Hit{}, Stats: stats, Warnings: warnings}, nil
			}
		}
	}

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
		Collection: collectionTextChunks,
		Dense:      emb.Dense,
		Sparse:     emb.Sparse,
		Filter: &QdrantFilter{
			RootIDsAny:   req.Filters.RootIDs,
			MimeIn:       mimeIn,
			KindIn:       req.Filters.KindIn,
			LangIn:       req.Filters.LangIn,
			MtimeAfterMs: req.Filters.MtimeAfterMs,
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

	// 3. Rerank the top RerankerCandidates hits (with fallback). Hits beyond
	//    the budget, and hits the reranker returned no score for, keep their
	//    vector rank and are placed *behind* the reranked block: cross-encoder
	//    scores and vector similarities are not comparable, so the two must
	//    never be interleaved in one sort.
	if req.Rerank && s.RerankerDisabled {
		warnings = append(warnings, "rerank_disabled")
	}
	if req.Rerank && !s.RerankerDisabled && len(hits) > 0 {
		budget := s.RerankerCandidates
		if budget <= 0 || budget > len(hits) {
			budget = len(hits)
		}
		t = time.Now()
		cands := make([]RerankCandidate, 0, budget)
		for _, h := range hits[:budget] {
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
			// keep score = raw_score, vector order for all hits
		} else {
			scoreByID := make(map[string]float64, len(rr.Scores))
			for _, sc := range rr.Scores {
				scoreByID[sc.ID] = sc.Score
			}
			ranked := make([]Hit, 0, budget)
			unranked := make([]Hit, 0, len(hits)-budget)
			for i, h := range hits {
				if sc, ok := scoreByID[h.PointID]; ok && i < budget {
					h.Score = sc
					ranked = append(ranked, h)
				} else {
					unranked = append(unranked, h)
				}
			}
			sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
			hits = append(ranked, unranked...)
		}
	}

	// 4b. Either group into file-level results, or truncate to top_k chunks.
	var order []string // file IDs in rank order — only populated when grouping
	if req.GroupByFile {
		byFile := make(map[string][]Hit)
		for _, h := range hits {
			chunks := byFile[h.FileID]
			if chunks == nil {
				if len(order) >= topK {
					continue // already collected top_k files
				}
				order = append(order, h.FileID)
			}
			if len(chunks) < maxChunks {
				byFile[h.FileID] = append(chunks, h)
			}
		}
		flat := make([]Hit, 0, len(hits))
		for _, fid := range order {
			flat = append(flat, byFile[fid]...)
		}
		hits = flat
	} else if len(hits) > topK {
		hits = hits[:topK]
	}

	// 5. Expand paths: documents via Parser, album assets via Photos.
	//    photos:<asset_id> ids are caption vectors Parser has never heard of
	//    (ExpandFiles would only list them as missing), so they are split off
	//    and resolved against Photos instead — same paths[0] slot, so every
	//    consumer (UI, agent tool, MCP) sees a real file name either way.
	t = time.Now()
	if len(hits) > 0 {
		idSet := make(map[string]struct{})
		docIDs := []string{}
		photoIDs := []string{}
		for _, h := range hits {
			if _, ok := idSet[h.FileID]; ok {
				continue
			}
			idSet[h.FileID] = struct{}{}
			if strings.HasPrefix(h.FileID, photoFileIDPrefix) {
				photoIDs = append(photoIDs, h.FileID)
			} else {
				docIDs = append(docIDs, h.FileID)
			}
		}
		if len(docIDs) > 0 {
			exp, err := s.Parser.ExpandFiles(ctx, docIDs)
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
		if len(photoIDs) > 0 && s.Photos != nil {
			assets, failed := s.expandPhotoPaths(ctx, photoIDs, req.UserID)
			if failed {
				warnings = append(warnings, "photo_expand_unavailable")
			}
			for i := range hits {
				if a, ok := assets[hits[i].FileID]; ok {
					var mtime int64
					if a.TakenAt != nil {
						mtime = a.TakenAt.UnixMilli()
					}
					hits[i].Paths = []FilePath{{RootID: photoRootID, Path: a.FilePath, MtimeMs: mtime}}
					if hits[i].Mime == "" {
						hits[i].Mime = a.MimeType
					}
				}
			}
		}
		stats.ExpandMs = int(time.Since(t).Milliseconds())
	}

	// 6. When grouping, assemble file-level results from the path-expanded hits.
	//    Full-scan per file (hits is small, ≤100) rather than relying on hits
	//    staying contiguously ordered by FileID — robust to any future change in
	//    how path expansion mutates/reorders the slice. Chunks keep score order
	//    because `hits` is sorted desc and we append in that order.
	var files []FileGroup
	if req.GroupByFile {
		files = make([]FileGroup, 0, len(order))
		for _, fid := range order {
			grp := FileGroup{FileID: fid}
			for _, h := range hits {
				if h.FileID == fid {
					grp.Chunks = append(grp.Chunks, h)
				}
			}
			if len(grp.Chunks) == 0 {
				continue
			}
			grp.Paths = grp.Chunks[0].Paths
			grp.Mime = grp.Chunks[0].Mime
			grp.Kind = grp.Chunks[0].Kind
			grp.Score = grp.Chunks[0].Score
			files = append(files, grp)
		}
	}

	// Hits is always populated (even on the grouped path) for backward compatibility;
	// consumers should prefer Files when present (len > 0).
	return &SearchResponse{Hits: hits, Files: files, Stats: stats, Warnings: warnings}, nil
}

// photoFileIDPrefix marks caption vectors written by the Photos→Parser
// pipeline: file_id = "photos:<asset_id>" (NimoOS-Parser write-side
// convention, see also AuthzService.PhotoCaption). photoRootID is the virtual
// root core seeds for them in o_root_grants and the root_ids they carry.
const (
	photoFileIDPrefix = "photos:"
	photoRootID       = "photos"
)

// photoLookupTimeout bounds each per-asset Photos call. Path expansion is
// cosmetic (a name for the card), so a slow Photos must not hold the search.
const photoLookupTimeout = 2 * time.Second

// photoLookupConcurrency caps in-flight GetAsset calls per search.
const photoLookupConcurrency = 4

// expandPhotoPaths resolves photos:<asset_id> file ids to their assets,
// concurrently and fail-open. The returned map is keyed by the full file id.
// failed is true when at least one lookup failed for a reason other than
// "asset gone" (ErrPhotoNotFound) — that is the caller's cue to warn once
// about Photos being unavailable; a deleted asset is just a miss.
func (s *SearchService) expandPhotoPaths(ctx context.Context, fileIDs []string, userID string) (map[string]*PhotoAsset, bool) {
	var (
		mu     sync.Mutex
		out    = make(map[string]*PhotoAsset, len(fileIDs))
		failed bool
		wg     sync.WaitGroup
		sem    = make(chan struct{}, photoLookupConcurrency)
	)
	for _, fid := range fileIDs {
		wg.Add(1)
		go func(fid string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, photoLookupTimeout)
			defer cancel()
			a, err := s.Photos.GetAsset(cctx, strings.TrimPrefix(fid, photoFileIDPrefix), userID)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				out[fid] = a
			case errors.Is(err, ErrPhotoNotFound):
				// gone from the library; keep paths=null silently
			default:
				failed = true
			}
		}(fid)
	}
	wg.Wait()
	return out, failed
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
		Collection:   collectionTextChunks,
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
