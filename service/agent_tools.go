package service

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
