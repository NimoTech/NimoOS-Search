package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentToolsSchema_HasBothTools(t *testing.T) {
	tools := &AgentTools{}
	b, err := json.Marshal(tools.ToolsSchema())
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	arr := got["tools"].([]any)
	require.Len(t, arr, 2)
	names := []string{}
	for _, t := range arr {
		names = append(names, t.(map[string]any)["name"].(string))
	}
	require.ElementsMatch(t, []string{"nimoos_search", "read_file_chunk"}, names)
}

func TestAgentTools_FiltersSchemaIsObject(t *testing.T) {
	tools := &AgentTools{}
	fs := tools.FiltersSchema()
	require.Contains(t, fs, "root_ids")
	require.Contains(t, fs, "mime_prefix")
}

func TestAgentInvoke_nimoosSearch_TrimsResponse(t *testing.T) {
	search := &SearchService{
		Parser: &fakeParserA{},
		Qdrant: &fakeQdrantA{hits: []QdrantHit{
			{PointID: "p1", Score: 0.5, Payload: map[string]any{
				"file_id": "f1", "kind": "body", "chunk_no": int64(0),
				"text": strings.Repeat("hello ", 100),
			}},
		}},
		Cache: NewEmbedCache(10, 0),
		DefaultTopK: 5, RerankerCandidates: 40,
	}
	tools := &AgentTools{Search: search}
	resp, err := tools.Invoke(context.Background(), "nimoos_search",
		map[string]any{"query": "hi", "top_k": float64(3)}, []string{"r1"})
	require.NoError(t, err)
	hits := resp["hits"].([]any)
	require.Len(t, hits, 1)
	h0 := hits[0].(map[string]any)
	preview := h0["preview"].(map[string]any)
	require.LessOrEqual(t, len(preview["text"].(string)), 200,
		"preview text should be trimmed to 200 chars for agent context")
}

type fakeParserA struct{}

func (fakeParserA) Embed(ctx context.Context, m, t, x, b string) (*EmbedResult, error) {
	return &EmbedResult{Dense: []float32{0.1}, ModelVersion: "v"}, nil
}
func (fakeParserA) Rerank(ctx context.Context, q string, c []RerankCandidate, k *int) (*RerankResult, error) {
	return &RerankResult{Scores: []RerankScore{}}, nil
}
func (fakeParserA) ExpandFiles(ctx context.Context, ids []string) (*ExpandFilesResult, error) {
	return &ExpandFilesResult{Files: []FileRecord{
		{FileID: "f1", Paths: []FilePath{
			{RootID: "r1", Path: "/a.md"},
			{RootID: "r1", Path: "/b.md"},
			{RootID: "r1", Path: "/c.md"},
			{RootID: "r1", Path: "/d.md"},
		}, Mime: "text/markdown"},
	}}, nil
}

type fakeQdrantA struct{ hits []QdrantHit }

func (f fakeQdrantA) SearchTextHybrid(context.Context, QdrantSearchRequest) ([]QdrantHit, error) {
	return f.hits, nil
}
func (f fakeQdrantA) ScrollByFileID(context.Context, string, string, []string, int, string) ([]QdrantHit, string, error) {
	return nil, "", nil
}
func (f fakeQdrantA) Count(context.Context, string) (uint64, error) { return 0, nil }
