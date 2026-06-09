package service

import (
	"context"
	"errors"
)

// AgentTools generates the OpenAI function-calling schema served at
// /v1/search/agent/tools and dispatches /v1/search/agent/tool invocations.
type AgentTools struct {
	Agg   *Aggregator
	Authz *AuthzService
}

func (a *AgentTools) ToolsSchema() map[string]any {
	return map[string]any{"tools": []any{
		map[string]any{
			"name":        "nimoos_search",
			"description": "Unified search over the user's NAS: by content (semantic), by filename, and photos — returns grouped candidates for the user to pick from. Narrow with `sources`, e.g. [\"images\"] for photos only.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"sources": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string", "enum": []string{"semantic", "filenames", "images"}},
						"description": "Which sources to search; omit for all three. Pass [\"images\"] to search photos only.",
					},
					"filters": map[string]any{"type": "object", "description": "Applies to the semantic source only."},
					"top_k":   map[string]any{"type": "integer", "default": 5, "minimum": 1, "maximum": 20, "description": "Per-source cap."},
				},
				"required": []string{"query"},
			},
		},
		map[string]any{
			"name":        "read_file_chunk",
			"description": "Fetch the chunk at (file_id, kind, chunk_no) plus a small window of neighboring chunks.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_id":  map[string]any{"type": "string"},
					"kind":     map[string]any{"type": "string", "enum": []string{"body", "ocr", "caption", "transcript", "summary"}},
					"chunk_no": map[string]any{"type": "integer"},
					"window":   map[string]any{"type": "integer", "default": 2, "maximum": 5},
				},
				"required": []string{"file_id", "kind", "chunk_no"},
			},
		},
	}}
}

func (a *AgentTools) FiltersSchema() map[string]any {
	return map[string]any{
		"root_ids":       map[string]any{"type": "string[]", "description": "Restrict to specific Wiki Roots. Intersected with user's allowed roots."},
		"mime_prefix":    map[string]any{"type": "string[]", "description": "Match by MIME prefix, e.g. ['text/markdown']."},
		"kind_in":        map[string]any{"type": "string[]", "description": "Chunk kind: body, ocr, caption, transcript, summary. MVP only has 'body'."},
		"lang_in":        map[string]any{"type": "string[]", "description": "ISO lang codes, e.g. ['zh','en']."},
		"mtime_after_ms": map[string]any{"type": "integer", "description": "Unix millisecond timestamp. Only return files modified after this time."},
	}
}

const (
	AgentMaxPaths       = 3
	AgentMaxPreviewChar = 200
)

// Invoke dispatches an agent tool by name. allowedRoots is the result of
// WikiClient.UserRoots(ctx, user_id) — route handler injects it so the tool
// invocation itself can't escape root scope.
func (a *AgentTools) Invoke(ctx context.Context, name string,
	args map[string]any, allowedRoots []string) (any, error) {
	switch name {
	case "nimoos_search":
		query, _ := args["query"].(string)
		var sources []string
		if raw, ok := args["sources"].([]any); ok {
			for _, v := range raw {
				if s, ok := v.(string); ok {
					sources = append(sources, s)
				}
			}
		}
		var f *Filters
		if rawFilters, ok := args["filters"].(map[string]any); ok && rawFilters != nil {
			f = parseFiltersMap(rawFilters)
		}
		uid, _ := args["__user_id"].(string) // optional; route may inject
		return a.Agg.Aggregate(ctx, AggregateRequest{
			Query: query, Sources: sources, Filters: f,
			AllowedRoots: allowedRoots, UserID: uid,
		}), nil
	case "read_file_chunk":
		// unchanged
		fileID, _ := args["file_id"].(string)
		kind, _ := args["kind"].(string)
		chunkNo := 0
		if v, ok := args["chunk_no"].(float64); ok {
			chunkNo = int(v)
		}
		window := 2
		if v, ok := args["window"].(float64); ok {
			window = int(v)
		}
		out, err := a.Authz.GetChunkWindow(ctx, fileID, kind, chunkNo, window, allowedRoots)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"file_id": out.FileID, "kind": out.Kind,
			"anchor_chunk_no": out.AnchorChunkNo, "chunks": out.Chunks,
		}, nil
	}
	return nil, errors.New("unknown tool: " + name)
}

func parseFiltersMap(m map[string]any) *Filters {
	f := &Filters{}
	if a, ok := m["root_ids"].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				f.RootIDs = append(f.RootIDs, s)
			}
		}
	}
	if a, ok := m["mime_prefix"].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				f.MimePrefix = append(f.MimePrefix, s)
			}
		}
	}
	if a, ok := m["kind_in"].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				f.KindIn = append(f.KindIn, s)
			}
		}
	}
	if a, ok := m["lang_in"].([]any); ok {
		for _, v := range a {
			if s, ok := v.(string); ok {
				f.LangIn = append(f.LangIn, s)
			}
		}
	}
	if v, ok := m["mtime_after_ms"].(float64); ok {
		f.MtimeAfterMs = int64(v)
	}
	return f
}

// trimHits caps paths/preview per hit and drops payload_extra. Shared by the
// agent tool's semantic group and the legacy single-source response.
func trimHits(r *SearchResponse) []any {
	hits := make([]any, 0, len(r.Hits))
	for _, h := range r.Hits {
		paths := h.Paths
		if len(paths) > AgentMaxPaths {
			paths = paths[:AgentMaxPaths]
		}
		text := ""
		if h.Preview.Text != nil {
			text = *h.Preview.Text
			if len(text) > AgentMaxPreviewChar {
				text = text[:AgentMaxPreviewChar]
			}
		}
		hits = append(hits, map[string]any{
			"score":   h.Score,
			"file_id": h.FileID,
			"paths":   paths,
			"mime":    h.Mime,
			"kind":    h.Kind,
			"cite":    h.Cite,
			"preview": map[string]any{"text": text, "thumbnail_url": nil},
		})
	}
	return hits
}
