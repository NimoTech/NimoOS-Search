package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/NimoTech/NimoOS-Search/service/fileindex"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestGetAgentTools(t *testing.T) {
	deps := &Deps{Tools: &service.AgentTools{}}
	e := echo.New()
	RegisterAgent(e, deps)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/agent/tools", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Len(t, got["tools"].([]any), 3)
}

func TestPostAgentTool_UnknownReturns400(t *testing.T) {
	deps := &Deps{
		Tools:  &service.AgentTools{},
		NimoOS: &stubNimoOS{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterAgent(e, deps)
	body, _ := json.Marshal(map[string]any{"name": "nope", "arguments": map[string]any{}})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/agent/tool", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAgentFiltersSchema(t *testing.T) {
	deps := &Deps{Tools: &service.AgentTools{}}
	e := echo.New()
	RegisterAgent(e, deps)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/agent/filters-schema", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestPostAgentTool_MissingUserIDReturns400(t *testing.T) {
	deps := &Deps{
		Tools:  &service.AgentTools{},
		NimoOS: &stubNimoOS{roots: []string{"r1"}},
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterAgent(e, deps)
	body, _ := json.Marshal(map[string]any{
		"name": "nimoos_search", "arguments": map[string]any{"query": "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/agent/tool", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No X-NimoOS-User-ID header.
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "X-NimoOS-User-ID required")
}

func TestPostAgentTool_EmptyRootsReturns200(t *testing.T) {
	st, err := service.LoadSettingsStore(t.TempDir()+"/s.json", service.SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15,
	})
	require.NoError(t, err)
	deps := &Deps{
		// Agg with nil Search: Aggregator skips semantic source, returns empty groups, 200 OK.
		Tools:  &service.AgentTools{Agg: &service.Aggregator{Settings: st}, Authz: nil},
		NimoOS: &stubNimoOS{roots: nil}, // user known, but no accessible roots
	}
	e := echo.New()
	e.Use(InjectUserID)
	RegisterAgent(e, deps)
	body, _ := json.Marshal(map[string]any{
		"name": "nimoos_search", "arguments": map[string]any{"query": "hi"},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/agent/tool", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var got map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	require.Contains(t, got, "groups", "response should contain grouped results")
}

// scopedFileIndex is a FileNameSearcher that records the scope it was given.
type scopedFileIndex struct{ got *[]string }

func (f scopedFileIndex) SearchWithin(_ context.Context, _ string, _ int, scope []string) ([]fileindex.FileNameHit, error) {
	*f.got = scope
	return []fileindex.FileNameHit{{Path: scope[0] + "/hit.txt", Name: "hit.txt"}}, nil
}
func (f scopedFileIndex) Status() string { return "ready" }

func agentToolReq(t *testing.T, e *echo.Echo) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"name": "nimoos_search", "arguments": map[string]any{"query": "hit", "sources": []string{"filenames"}}})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/agent/tool", bytes.NewReader(body))
	req.Header.Set("X-NimoOS-User-ID", "u1")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func newAgentDeps(t *testing.T, nimoos *stubNimoOS, fi service.FileNameSearcher) *Deps {
	t.Helper()
	st, err := service.LoadSettingsStore(t.TempDir()+"/s.json", service.SearchSettings{
		DefaultSources: []string{"filenames"}, SemanticTopK: 5, FilenameTopK: 5, ImageTopK: 5, NotesTopK: 5, MaxTotalResults: 15,
	})
	require.NoError(t, err)
	agg := &service.Aggregator{FileIndex: fi, Settings: st}
	return &Deps{Tools: &service.AgentTools{Agg: agg}, NimoOS: nimoos}
}

func TestPostAgentTool_FilenamesScopedByGrantedPaths(t *testing.T) {
	var got []string
	deps := newAgentDeps(t, &stubNimoOS{roots: []string{"r1"}, paths: []string{"/DATA/docs"}}, scopedFileIndex{got: &got})
	e := echo.New()
	e.Use(InjectUserID)
	RegisterAgent(e, deps)
	rec := agentToolReq(t, e)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"/DATA/docs"}, got, "route must hand the user's granted root paths to the filename search")
}

func TestPostAgentTool_OldCoreWithoutPaths_DegradesNot503(t *testing.T) {
	var got []string
	deps := newAgentDeps(t, &stubNimoOS{roots: []string{"r1"}, pathsErr: service.ErrRootPathsUnavailable}, scopedFileIndex{got: &got})
	e := echo.New()
	e.Use(InjectUserID)
	RegisterAgent(e, deps)
	rec := agentToolReq(t, e)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "filenames_scope_unavailable")
	require.Nil(t, got, "filename index must not be searched without a scope")
}
