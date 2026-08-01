package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type stubNimoOS struct{ roots []string }

func (s *stubNimoOS) SearchRoots(ctx context.Context, uid string) ([]string, error) {
	return s.roots, nil
}

func TestPostSearchText_HappyPath(t *testing.T) {
	search := &service.SearchService{
		Parser: &fakeParserSvc{},
		Qdrant: &fakeQdrantSvc{hits: []service.QdrantHit{
			{PointID: "p1", Score: 0.7, Payload: map[string]any{
				"file_id": "f1", "kind": "body", "chunk_no": int64(0), "text": "hello",
			}},
		}},
		Cache:       service.NewEmbedCache(10, 0),
		DefaultTopK: 5, RerankerCandidates: 40, ParserVersion: "v",
	}
	deps := &Deps{Search: search, NimoOS: &stubNimoOS{roots: []string{"r1"}}}

	e := echo.New()
	e.Use(InjectUserID)
	RegisterText(e, deps)

	body, _ := json.Marshal(map[string]any{"query": "hi", "top_k": 5})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/text", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.SearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Hits, 1)
	require.Equal(t, "f1", resp.Hits[0].FileID)
}

func TestPostSearchText_NoAccessibleRoots(t *testing.T) {
	search := &service.SearchService{
		Parser: &fakeParserSvc{}, Qdrant: &fakeQdrantSvc{},
		Cache: service.NewEmbedCache(10, 0), DefaultTopK: 5, RerankerCandidates: 40,
	}
	deps := &Deps{Search: search, NimoOS: &stubNimoOS{roots: []string{}}}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterText(e, deps)

	body, _ := json.Marshal(map[string]any{"query": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/text", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.SearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(t, resp.Hits)
	require.Contains(t, resp.Warnings, "no_accessible_roots")
}

// TestPostSearchText_PhotosRootPassesScope asserts that when the allowed set
// contains the core seed root "photos" (request doesn't explicitly specify
// root_ids), ApplyScope doesn't filter it out - i.e. a caption in
// text_chunks with root_ids=["photos"] can still be hit by semantic search.
// This is the final acceptance point of the "authorization source
// decoupling" project (see task-8-brief.md).
func TestPostSearchText_PhotosRootPassesScope(t *testing.T) {
	q := &capturingQdrantSvc{hits: []service.QdrantHit{
		{PointID: "p1", Score: 0.9, Payload: map[string]any{
			"file_id": "photos", "kind": "caption", "chunk_no": int64(0), "text": "a cat on a windowsill",
		}},
	}}
	search := &service.SearchService{
		Parser: &fakeParserSvc{}, Qdrant: q,
		Cache: service.NewEmbedCache(10, 0), DefaultTopK: 5, RerankerCandidates: 40, ParserVersion: "v",
	}
	deps := &Deps{Search: search, NimoOS: &stubNimoOS{roots: []string{"photos"}}}

	e := echo.New()
	e.Use(InjectUserID)
	RegisterText(e, deps)

	body, _ := json.Marshal(map[string]any{"query": "cat"})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/text", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, q.gotRootIDs, "photos", "ApplyScope should put photos into the root filter sent to Qdrant")
	var resp service.SearchResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(t, resp.Hits, 1)
	require.NotContains(t, resp.Warnings, "no_accessible_roots")
}

// Minimal stubs reusing service package types (in service_test.go these are
// named differently to avoid collision; here we declare them in test scope).
type fakeParserSvc struct{}

func (f *fakeParserSvc) Embed(ctx context.Context, m, t, x, b string) (*service.EmbedResult, error) {
	return &service.EmbedResult{Dense: []float32{0.1}, ModelVersion: "v"}, nil
}
func (f *fakeParserSvc) Rerank(ctx context.Context, q string, c []service.RerankCandidate, k *int) (*service.RerankResult, error) {
	return &service.RerankResult{Scores: []service.RerankScore{}}, nil
}
func (f *fakeParserSvc) ExpandFiles(ctx context.Context, ids []string) (*service.ExpandFilesResult, error) {
	return &service.ExpandFilesResult{}, nil
}

type fakeQdrantSvc struct{ hits []service.QdrantHit }

func (f *fakeQdrantSvc) SearchTextHybrid(ctx context.Context, r service.QdrantSearchRequest) ([]service.QdrantHit, error) {
	return f.hits, nil
}
func (f *fakeQdrantSvc) ScrollByFileID(ctx context.Context, c, fid string, roots []string, l int, off string) ([]service.QdrantHit, string, error) {
	return nil, "", nil
}
func (f *fakeQdrantSvc) Count(ctx context.Context, c string) (uint64, error) { return 0, nil }

// capturingQdrantSvc records the incoming root filter condition, used to
// assert that ApplyScope's result actually made it into the Qdrant query
// (not just into the Filters struct itself).
type capturingQdrantSvc struct {
	hits       []service.QdrantHit
	gotRootIDs []string
}

func (f *capturingQdrantSvc) SearchTextHybrid(ctx context.Context, r service.QdrantSearchRequest) ([]service.QdrantHit, error) {
	if r.Filter != nil {
		f.gotRootIDs = r.Filter.RootIDsAny
	}
	return f.hits, nil
}
func (f *capturingQdrantSvc) ScrollByFileID(ctx context.Context, c, fid string, roots []string, l int, off string) ([]service.QdrantHit, string, error) {
	return nil, "", nil
}
func (f *capturingQdrantSvc) Count(ctx context.Context, c string) (uint64, error) { return 0, nil }
