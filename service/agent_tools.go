package service

import (
	"context"
	"errors"
)

// AgentTools exposes LLM-callable tools for NimoOS-Search.
// T17 adds schema methods; T18 adds Invoke.
type AgentTools struct {
	Search *SearchService
	Authz  *AuthzService
}

// ToolsSchema returns the JSON-serialisable schema advertised to the LLM agent.
// The shape is {"tools": [...]}, where each entry is an OpenAI-style function
// tool descriptor.
func (a *AgentTools) ToolsSchema() map[string]any {
	return map[string]any{
		"tools": []any{
			map[string]any{
				"name":        "nimoos_search",
				"description": "Full-text + vector hybrid search over files indexed in NimoOS. Returns scored hits with file paths and text previews.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Natural-language search query.",
						},
						"top_k": map[string]any{
							"type":        "integer",
							"description": "Maximum number of hits to return (default 5).",
						},
						"filters": map[string]any{
							"type":        "object",
							"description": "Optional search filters (root_ids, mime_prefix, kind_in, lang_in).",
							"properties":  a.FiltersSchema(),
						},
					},
					"required": []string{"query"},
				},
			},
			map[string]any{
				"name":        "read_file_chunk",
				"description": "Fetch a specific text chunk from an indexed file by file_id, kind, and chunk_no.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"file_id": map[string]any{
							"type":        "string",
							"description": "The file ID returned in a search hit.",
						},
						"kind": map[string]any{
							"type":        "string",
							"description": "Chunk kind: body, ocr, caption, transcript, or summary.",
						},
						"chunk_no": map[string]any{
							"type":        "integer",
							"description": "Zero-based chunk index.",
						},
						"window": map[string]any{
							"type":        "integer",
							"description": "Number of surrounding chunks to include on each side (default 1).",
						},
					},
					"required": []string{"file_id", "kind", "chunk_no"},
				},
			},
		},
	}
}

// FiltersSchema returns a JSON-schema properties map describing the Filters
// object, for embedding inside ToolsSchema and for direct exposure on the
// /v1/agent/filters-schema endpoint.
func (a *AgentTools) FiltersSchema() map[string]any {
	return map[string]any{
		"root_ids": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Restrict results to these storage root IDs.",
		},
		"mime_prefix": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Restrict results to MIME types matching these prefixes (e.g. [\"text/\", \"image/\"]).",
		},
		"kind_in": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Restrict results to specific chunk kinds (body, ocr, caption, transcript, summary).",
		},
		"lang_in": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "Restrict results to specific language codes (BCP-47).",
		},
	}
}

const (
	// AgentMaxPaths is the maximum number of file paths included per hit in an
	// agent response (trimmed to keep context windows small).
	AgentMaxPaths = 3
	// AgentMaxPreviewChar is the maximum character length of the preview text
	// included in an agent response.
	AgentMaxPreviewChar = 200
)

// Invoke dispatches a named tool call with the given args under allowedRoots
// authz scope and returns a trimmed map suitable for injection into an LLM
// context window.
//
// Supported names: "nimoos_search", "read_file_chunk".
func (a *AgentTools) Invoke(ctx context.Context, name string,
	args map[string]any, allowedRoots []string) (map[string]any, error) {
	switch name {
	case "nimoos_search":
		if a.Search == nil {
			return nil, errors.New("search service not available")
		}
		query, _ := args["query"].(string)
		if query == "" {
			return nil, errors.New("nimoos_search: missing required argument 'query'")
		}
		topK := 0
		if v, ok := args["top_k"]; ok {
			switch n := v.(type) {
			case float64:
				topK = int(n)
			case int:
				topK = n
			}
		}

		// Build filters from args["filters"] if present, then enforce scope.
		var filters *Filters
		if fm, ok := args["filters"].(map[string]any); ok {
			filters = parseFiltersMap(fm)
		}
		if filters == nil {
			filters = &Filters{}
		}
		var warning string
		filters, warning = ApplyScope(filters, allowedRoots)
		if warning == "no_accessible_roots" {
			return map[string]any{"hits": []any{}, "warnings": []string{"no_accessible_roots"}}, nil
		}

		resp, err := a.Search.SearchText(ctx, SearchRequest{
			Query:   query,
			Filters: filters,
			TopK:    topK,
		})
		if err != nil {
			return nil, err
		}
		return trimSearchResponseForAgent(resp), nil

	case "read_file_chunk":
		if a.Authz == nil {
			return nil, errors.New("authz service not available")
		}
		fileID, _ := args["file_id"].(string)
		kind, _ := args["kind"].(string)
		if fileID == "" || kind == "" {
			return nil, errors.New("read_file_chunk: file_id and kind are required")
		}
		chunkNo := 0
		if v, ok := args["chunk_no"]; ok {
			switch n := v.(type) {
			case float64:
				chunkNo = int(n)
			case int:
				chunkNo = n
			}
		}
		window := 1
		if v, ok := args["window"]; ok {
			switch n := v.(type) {
			case float64:
				window = int(n)
			case int:
				window = n
			}
		}
		cr, err := a.Authz.GetChunkWindow(ctx, fileID, kind, chunkNo, window, allowedRoots)
		if err != nil {
			return nil, err
		}
		chunks := make([]any, 0, len(cr.Chunks))
		for _, c := range cr.Chunks {
			text := c.Text
			if len(text) > AgentMaxPreviewChar {
				text = text[:AgentMaxPreviewChar]
			}
			chunks = append(chunks, map[string]any{
				"chunk_no": c.ChunkNo,
				"text":     text,
			})
		}
		return map[string]any{
			"file_id":         cr.FileID,
			"kind":            cr.Kind,
			"anchor_chunk_no": cr.AnchorChunkNo,
			"chunks":          chunks,
		}, nil

	default:
		return nil, errors.New("unknown tool: " + name)
	}
}

// parseFiltersMap converts a loosely-typed map (as received from JSON args) to
// a *Filters struct.
func parseFiltersMap(m map[string]any) *Filters {
	f := &Filters{}
	if v, ok := m["root_ids"]; ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.RootIDs = append(f.RootIDs, s)
				}
			}
		}
	}
	if v, ok := m["mime_prefix"]; ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.MimePrefix = append(f.MimePrefix, s)
				}
			}
		}
	}
	if v, ok := m["kind_in"]; ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.KindIn = append(f.KindIn, s)
				}
			}
		}
	}
	if v, ok := m["lang_in"]; ok {
		if arr, ok := v.([]any); ok {
			for _, e := range arr {
				if s, ok := e.(string); ok {
					f.LangIn = append(f.LangIn, s)
				}
			}
		}
	}
	return f
}

// trimSearchResponseForAgent converts a *SearchResponse into a compact
// map[string]any with paths trimmed to AgentMaxPaths and preview text trimmed
// to AgentMaxPreviewChar.
func trimSearchResponseForAgent(r *SearchResponse) map[string]any {
	hits := make([]any, 0, len(r.Hits))
	for _, h := range r.Hits {
		// Trim paths.
		paths := h.Paths
		if len(paths) > AgentMaxPaths {
			paths = paths[:AgentMaxPaths]
		}
		pathList := make([]any, 0, len(paths))
		for _, p := range paths {
			pathList = append(pathList, map[string]any{
				"root_id": p.RootID,
				"path":    p.Path,
			})
		}

		// Trim preview text.
		previewText := ""
		if h.Preview.Text != nil {
			previewText = *h.Preview.Text
		}
		if len(previewText) > AgentMaxPreviewChar {
			previewText = previewText[:AgentMaxPreviewChar]
		}

		hits = append(hits, map[string]any{
			"score":    h.Score,
			"file_id":  h.FileID,
			"mime":     h.Mime,
			"kind":     h.Kind,
			"chunk_no": h.Cite.ChunkNo,
			"paths":    pathList,
			"preview": map[string]any{
				"text": previewText,
			},
		})
	}
	return map[string]any{
		"hits":     hits,
		"warnings": r.Warnings,
	}
}
