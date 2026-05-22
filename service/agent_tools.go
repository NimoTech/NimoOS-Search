package service

import (
	"context"
	"errors"
)

// AgentTools generates the OpenAI function-calling schema served at
// /v1/search/agent/tools and dispatches /v1/search/agent/tool invocations.
type AgentTools struct {
	Search *SearchService
	Authz  *AuthzService
}

func (a *AgentTools) ToolsSchema() map[string]any {
	return map[string]any{"tools": []any{
		map[string]any{
			"name":        "nimoos_search",
			"description": "Search the user's personal NAS for relevant content. Use this when the user asks about files, photos, videos, documents, or any past content stored on their NAS.",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"modality": map[string]any{
						"type": "string", "enum": []string{"auto", "text"}, "default": "auto",
						"description": "MVP supports text only.",
					},
					"filters": map[string]any{"type": "object"},
					"top_k":   map[string]any{"type": "integer", "default": 5, "maximum": 20},
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
		"root_ids":    map[string]any{"type": "string[]", "description": "Restrict to specific Wiki Roots. Intersected with user's allowed roots."},
		"mime_prefix": map[string]any{"type": "string[]", "description": "Match by MIME prefix, e.g. ['text/markdown']."},
		"kind_in":     map[string]any{"type": "string[]", "description": "Chunk kind: body, ocr, caption, transcript, summary. MVP only has 'body'."},
		"lang_in":     map[string]any{"type": "string[]", "description": "ISO lang codes, e.g. ['zh','en']."},
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
	args map[string]any, allowedRoots []string) (map[string]any, error) {
	switch name {
	case "nimoos_search":
		query, _ := args["query"].(string)
		topK := 5
		if v, ok := args["top_k"].(float64); ok {
			topK = int(v)
		}
		if topK > 20 {
			topK = 20
		}
		var f *Filters
		if rawFilters, ok := args["filters"].(map[string]any); ok && rawFilters != nil {
			f = parseFiltersMap(rawFilters)
		}
		scoped, warn := ApplyScope(f, allowedRoots)
		if warn == "no_accessible_roots" {
			return map[string]any{"hits": []any{}, "warnings": []string{warn}}, nil
		}
		resp, err := a.Search.SearchText(ctx, SearchRequest{
			Query: query, Filters: scoped, TopK: topK, Rerank: true,
		})
		if err != nil {
			return nil, err
		}
		return trimSearchResponseForAgent(resp), nil
	case "read_file_chunk":
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
	return f
}

// trimSearchResponseForAgent caps paths to AgentMaxPaths and preview text
// to AgentMaxPreviewChar, drops payload_extra entirely. Saves tokens.
func trimSearchResponseForAgent(r *SearchResponse) map[string]any {
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
	return map[string]any{
		"hits":     hits,
		"stats":    r.Stats,
		"warnings": r.Warnings,
	}
}
