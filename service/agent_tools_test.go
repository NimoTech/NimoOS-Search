package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAgentToolsSchema_NimoosSearchUsesSourcesNotModality(t *testing.T) {
	tools := &AgentTools{}
	arr := tools.ToolsSchema()["tools"].([]any)
	require.Len(t, arr, 3)
	var search map[string]any
	for _, x := range arr {
		m := x.(map[string]any)
		if m["name"] == "nimoos_search" {
			search = m
		}
	}
	require.NotNil(t, search)
	props := search["parameters"].(map[string]any)["properties"].(map[string]any)
	require.Contains(t, props, "sources")
	require.NotContains(t, props, "modality", "modality removed in favor of sources")
}

func TestAgentTools_FiltersSchemaIsObject(t *testing.T) {
	tools := &AgentTools{}
	fs := tools.FiltersSchema()
	require.Contains(t, fs, "root_ids")
	require.Contains(t, fs, "mime_prefix")
}

func TestAgentInvoke_nimoosSearch_ReturnsGroups(t *testing.T) {
	search := &SearchService{
		Parser: &fakeParserA{},
		Qdrant: &fakeQdrantA{hits: []QdrantHit{{PointID: "p1", Score: 0.5, Payload: map[string]any{
			"file_id": "f1", "kind": "body", "chunk_no": int64(0), "text": strings.Repeat("hello ", 100),
		}}}},
		Cache: NewEmbedCache(10, 0), DefaultTopK: 5, RerankerCandidates: 40,
	}
	st := &SettingsStore{cur: SearchSettings{DefaultSources: []string{"semantic", "filenames", "images"}, SemanticTopK: 5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15}}
	agg := &Aggregator{Search: search, Settings: st}
	tools := &AgentTools{Agg: agg}
	out, err := tools.Invoke(context.Background(), "nimoos_search",
		map[string]any{"query": "hi"}, []string{"r1"})
	require.NoError(t, err)
	resp := out.(*AggregateResponse)
	require.Len(t, resp.Groups.Semantic, 1)
	first := resp.Groups.Semantic[0].(map[string]any)
	require.LessOrEqual(t, len(first["preview"].(map[string]any)["text"].(string)), 200)
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

func TestAgentInvoke_readDocument_ReturnsStitchedText(t *testing.T) {
	q := &recordingQdrant{hits: []QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "hello", "offset_start": int64(0), "offset_end": int64(5), "root_ids": []any{"r1"}}},
	}}
	tools := &AgentTools{Authz: &AuthzService{Qdrant: q}}
	out, err := tools.Invoke(context.Background(), "read_document",
		map[string]any{"file_id": "f1"}, []string{"r1"})
	require.NoError(t, err)
	m := out.(map[string]any)
	require.Equal(t, "f1", m["file_id"])
	require.Equal(t, "hello", m["text"])
	require.Equal(t, false, m["truncated"])
}

func TestAgentToolsSchema_IncludesReadDocument(t *testing.T) {
	tools := &AgentTools{}
	arr := tools.ToolsSchema()["tools"].([]any)
	names := map[string]bool{}
	for _, x := range arr {
		names[x.(map[string]any)["name"].(string)] = true
	}
	require.True(t, names["read_document"])
}

func (f fakeQdrantA) DistinctValues(context.Context, string, string) ([]string, error) {
	return nil, nil
}
