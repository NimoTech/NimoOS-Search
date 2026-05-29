package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
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
	require.Len(t, got["tools"].([]any), 2)
}

func TestPostAgentTool_UnknownReturns400(t *testing.T) {
	deps := &Deps{
		Tools: &service.AgentTools{},
		Wiki:  &stubWiki{roots: []string{"r1"}},
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
		Tools: &service.AgentTools{},
		Wiki:  &stubWiki{roots: []string{"r1"}},
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
}

func TestPostAgentTool_EmptyRootsReturns200WithWarning(t *testing.T) {
	deps := &Deps{
		Tools: &service.AgentTools{Search: nil, Authz: nil},
		Wiki:  &stubWiki{roots: nil}, // user known, but no accessible roots
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
	require.Contains(t, got["warnings"], "no_accessible_roots")
}
