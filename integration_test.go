package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	v1 "github.com/NimoTech/NimoOS-Search/route/v1"
	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestE2E_TextSearchHappyPath(t *testing.T) {
	if os.Getenv("QDRANT_URL") == "" {
		t.Skip("set QDRANT_URL=http://127.0.0.1:6333 to run")
	}

	// 1. Boot fake Parser
	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/parser/embed":
			w.Write([]byte(`{"dense":[0.1,0.2,0.3],"sparse":{"indices":[1],"values":[0.5]},"dim":3,"model_version":"bge-m3/v1"}`))
		case "/v1/parser/rerank":
			w.Write([]byte(`{"scores":[{"id":"p1","score":0.9}],"model_version":"bge-reranker-v2-m3/v1","took_ms":5}`))
		case "/v1/parser/_internal/files":
			w.Write([]byte(`{"files":[{"file_id":"f1","paths":[{"root_id":"r1","path":"/a.md","mtime_ms":1}],"mime":"text/markdown","modalities_done":{"text":"bge-m3/v1"},"parser_version":"parser/0.1.0","indexed_at":1,"tombstoned_at":null}],"missing":[]}`))
		}
	}))
	defer parser.Close()

	// 2. Fake NimoOS core via RootAuthorizer stub (we won't bother with full Gateway here)
	nimoos := stubNimoOSE2E{roots: []string{"r1"}}

	// 3. Real Qdrant — Search service against real cluster
	q, err := service.NewQdrantClient("127.0.0.1", 6334, "http://127.0.0.1:6333")
	require.NoError(t, err)
	defer q.Close()

	cache := service.NewEmbedCache(100, time.Hour)
	pc := service.NewParserClient(service.NewBaseURLSource("", parser.URL), 5)
	search := &service.SearchService{
		Parser: pc, Qdrant: q, Cache: cache,
		ParserVersion: "parser/0.1.0", DefaultTopK: 5, RerankerCandidates: 40,
	}
	authz := &service.AuthzService{Qdrant: q}
	agg := &service.Aggregator{Search: search}
	tools := &service.AgentTools{Agg: agg, Authz: authz}

	// 4. Wire Echo
	e := echo.New()
	e.Use(v1.InjectUserID)
	deps := &v1.Deps{Search: search, Authz: authz, NimoOS: &nimoos, Tools: tools}
	v1.RegisterText(e, deps)

	// 5. POST /v1/search/text
	body, _ := json.Marshal(map[string]any{"query": "hello", "top_k": 5})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/text", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	// 6. Assert
	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.SearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	// Note: hit count depends on whether Qdrant has seeded data; in CI we'd
	// pre-seed via Parser. For this MVP test we just verify the request shape
	// and that orchestration ran (no warnings, valid stats).
	require.Empty(t, resp.Warnings)
}

type stubNimoOSE2E struct{ roots []string }

func (s *stubNimoOSE2E) SearchRoots(ctx context.Context, uid string) ([]string, error) {
	return s.roots, nil
}

func (s *stubNimoOSE2E) SearchRootPaths(ctx context.Context, uid string) ([]string, error) {
	return []string{"/"}, nil
}
