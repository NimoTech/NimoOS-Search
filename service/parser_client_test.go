package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParserEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/parser/embed", r.URL.Path)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		require.Equal(t, "bge-m3", body["model"])
		require.Equal(t, "text", body["input_type"])
		require.Equal(t, "hello", body["text"])
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"dense": [0.1, 0.2, 0.3],
			"sparse": {"indices":[1,2], "values":[0.5,0.3]},
			"dim": 3,
			"model_version": "bge-m3/v1"
		}`))
	}))
	defer srv.Close()

	c := NewParserClient(NewBaseURLSource("", srv.URL), 5)
	out, err := c.Embed(context.Background(), "bge-m3", "text", "hello", "")
	require.NoError(t, err)
	require.Equal(t, []float32{0.1, 0.2, 0.3}, out.Dense)
	require.Equal(t, "bge-m3/v1", out.ModelVersion)
}

func TestParserRerank(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/parser/rerank", r.URL.Path)
		w.Write([]byte(`{
			"scores": [{"id":"a","score":0.9},{"id":"b","score":0.4}],
			"model_version": "bge-reranker-v2-m3/v1",
			"took_ms": 21
		}`))
	}))
	defer srv.Close()

	c := NewParserClient(NewBaseURLSource("", srv.URL), 5)
	out, err := c.Rerank(context.Background(), "q",
		[]RerankCandidate{{ID: "a", Text: "alpha"}, {ID: "b", Text: "beta"}}, nil)
	require.NoError(t, err)
	require.Len(t, out.Scores, 2)
	require.Equal(t, "a", out.Scores[0].ID)
	require.InDelta(t, 0.9, out.Scores[0].Score, 1e-6)
}

func TestParserRerank_503ReturnsErrUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := NewParserClient(NewBaseURLSource("", srv.URL), 5)
	_, err := c.Rerank(context.Background(), "q",
		[]RerankCandidate{{ID: "a", Text: "alpha"}}, nil)
	require.ErrorIs(t, err, ErrRerankerUnavailable)
}

func TestParserExpandFiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/parser/_internal/files", r.URL.Path)
		require.Contains(t, r.URL.RawQuery, "file_ids=a,b")
		w.Write([]byte(`{
			"files": [
				{"file_id":"a","paths":[{"root_id":"r1","path":"/x.md","mtime_ms":1}],
				 "mime":"text/markdown","modalities_done":{"text":"bge-m3/v1"},
				 "parser_version":"parser/0.1.0","indexed_at":100,"tombstoned_at":null}
			],
			"missing": ["b"]
		}`))
	}))
	defer srv.Close()
	c := NewParserClient(NewBaseURLSource("", srv.URL), 5)
	out, err := c.ExpandFiles(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, out.Files, 1)
	require.Equal(t, "a", out.Files[0].FileID)
	require.Equal(t, "/x.md", out.Files[0].Paths[0].Path)
	require.Equal(t, []string{"b"}, out.Missing)
}
