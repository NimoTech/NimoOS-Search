package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func newSettingsDeps(t *testing.T) *Deps {
	st := &service.SettingsStore{}
	// seed via Put through a temp store
	s, err := service.LoadSettingsStore(t.TempDir()+"/s.json", service.SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15,
		FileIndexEnabled: true, FileIndexRoots: []string{"/DATA"}, FileIndexScanIntervalH: 6,
	})
	require.NoError(t, err)
	st = s
	return &Deps{Settings: st, FileIndex: nil}
}

func TestGetSettings(t *testing.T) {
	e := echo.New()
	RegisterSettings(e, newSettingsDeps(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/search/settings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "semantic_top_k")
}

func TestPutSettings_AppliesAndRejectsEmptySources(t *testing.T) {
	e := echo.New()
	d := newSettingsDeps(t)
	RegisterSettings(e, d)
	// valid patch
	req := httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(`{"semantic_top_k":9}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 9, d.Settings.Get().SemanticTopK)
	// invalid: empty sources
	req = httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(`{"default_sources":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestStatus_DisabledWhenNoIndex(t *testing.T) {
	e := echo.New()
	RegisterSettings(e, newSettingsDeps(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/search/fileindex/status", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "disabled", body["status"])
}
