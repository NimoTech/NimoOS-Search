package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestGetFile_404WhenNotInScope(t *testing.T) {
	q := &emptyQdrant{}
	deps := &Deps{
		Authz: &service.AuthzService{Qdrant: q},
		Wiki:  &stubWiki{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterFile(e, deps)

	req := httptest.NewRequest(http.MethodGet, "/v1/search/file?file_id=secret", nil)
	req.Header.Set("X-NimoOS-User-ID", "u1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetFile_HappyPath(t *testing.T) {
	q := &fakeQdrantSvc{} // no hits — but we'll override
	q2 := &chunkScrollQdrant{hits: []service.QdrantHit{
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(0),
			"text": "alpha", "root_ids": []any{"r1"}}},
		{Payload: map[string]any{"file_id": "f1", "kind": "body", "chunk_no": int64(1),
			"text": "beta", "root_ids": []any{"r1"}}},
	}}
	_ = q
	deps := &Deps{
		Authz: &service.AuthzService{Qdrant: q2},
		Wiki:  &stubWiki{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterFile(e, deps)

	req := httptest.NewRequest(http.MethodGet, "/v1/search/file?file_id=f1", nil)
	req.Header.Set("X-NimoOS-User-ID", "u1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

type emptyQdrant struct{}

func (emptyQdrant) SearchTextHybrid(context.Context, service.QdrantSearchRequest) ([]service.QdrantHit, error) {
	return nil, nil
}
func (emptyQdrant) ScrollByFileID(context.Context, string, string, []string, int, string) ([]service.QdrantHit, string, error) {
	return nil, "", nil
}
func (emptyQdrant) Count(context.Context, string) (uint64, error) { return 0, nil }

type chunkScrollQdrant struct{ hits []service.QdrantHit }

func (c *chunkScrollQdrant) SearchTextHybrid(context.Context, service.QdrantSearchRequest) ([]service.QdrantHit, error) {
	return nil, nil
}
func (c *chunkScrollQdrant) ScrollByFileID(context.Context, string, string, []string, int, string) ([]service.QdrantHit, string, error) {
	return c.hits, "", nil
}
func (c *chunkScrollQdrant) Count(context.Context, string) (uint64, error) { return 0, nil }
