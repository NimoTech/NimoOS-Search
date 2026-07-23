package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestGetChunk_HappyPath(t *testing.T) {
	q := &chunkScrollQdrant{hits: []service.QdrantHit{
		{Payload: map[string]any{"kind": "body", "chunk_no": int64(5), "text": "five"}},
		{Payload: map[string]any{"kind": "body", "chunk_no": int64(6), "text": "six"}},
		{Payload: map[string]any{"kind": "body", "chunk_no": int64(7), "text": "seven"}},
		{Payload: map[string]any{"kind": "body", "chunk_no": int64(8), "text": "eight"}},
		{Payload: map[string]any{"kind": "body", "chunk_no": int64(9), "text": "nine"}},
	}}
	deps := &Deps{
		Authz:  &service.AuthzService{Qdrant: q},
		NimoOS: &stubNimoOS{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterChunk(e, deps)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/search/chunk?file_id=f1&kind=body&chunk_no=7&window=2", nil)
	req.Header.Set("X-NimoOS-User-ID", "u1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp service.ChunkContextResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(t, 7, resp.AnchorChunkNo)
	require.Len(t, resp.Chunks, 5)
}

func TestGetChunk_404OnNoScope(t *testing.T) {
	deps := &Deps{
		Authz:  &service.AuthzService{Qdrant: &emptyQdrant{}},
		NimoOS: &stubNimoOS{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterChunk(e, deps)
	req := httptest.NewRequest(http.MethodGet,
		"/v1/search/chunk?file_id=secret&kind=body&chunk_no=0", nil)
	req.Header.Set("X-NimoOS-User-ID", "u1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
