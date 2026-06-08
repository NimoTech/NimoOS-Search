package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/NimoTech/NimoOS-Search/service/fileindex"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func newSettingsDeps(t *testing.T) *Deps {
	st, err := service.LoadSettingsStore(t.TempDir()+"/s.json", service.SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15,
		FileIndexEnabled: true, FileIndexRoots: []string{"/DATA"}, FileIndexScanIntervalH: 6,
	})
	require.NoError(t, err)
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

func TestPutSettings_RestartRequiredOnlyForFileindexFields(t *testing.T) {
	e := echo.New()
	d := newSettingsDeps(t)
	RegisterSettings(e, d)
	// changing a restart field flags restart_required
	req := httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(`{"fileindex_scan_interval_h":12}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, true, body["restart_required"])
	// a runtime-only change does NOT require restart
	req = httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(`{"semantic_top_k":7}`))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, false, body["restart_required"])
}

func TestRescan_DisabledWhenNoIndex(t *testing.T) {
	e := echo.New()
	RegisterSettings(e, newSettingsDeps(t)) // FileIndex nil
	req := httptest.NewRequest(http.MethodPost, "/v1/search/fileindex/rescan", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestRescan_DisabledWhenEmptySubsystem(t *testing.T) {
	// fileindex disabled at startup yields a non-nil Subsystem with a nil Index;
	// rescan must still report 503, not a misleading started:true.
	e := echo.New()
	d := newSettingsDeps(t)
	d.FileIndex = &fileindex.Subsystem{}
	RegisterSettings(e, d)
	req := httptest.NewRequest(http.MethodPost, "/v1/search/fileindex/rescan", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestPutSettings_RootsChangeHotReloadsNoRestart(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "alpha.txt"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "bravo.txt"), []byte("x"), 0644))
	idx, err := fileindex.Open(filepath.Join(t.TempDir(), "fi.db"))
	require.NoError(t, err)
	sub := fileindex.NewSubsystem(idx, time.Hour, time.Hour, nil)
	t.Cleanup(func() { _ = sub.Close() })
	sub.StartInitial([]string{dirA})

	st, err := service.LoadSettingsStore(t.TempDir()+"/s.json", service.SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, MaxTotalResults: 15,
		FileIndexEnabled: true, FileIndexRoots: []string{dirA}, FileIndexScanIntervalH: 6,
	})
	require.NoError(t, err)
	d := &Deps{Settings: st, FileIndex: sub}

	e := echo.New()
	RegisterSettings(e, d)
	body := `{"fileindex_roots":["` + dirB + `"]}`
	req := httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["restart_required"], "roots change is hot, not restart")

	require.Eventually(t, func() bool {
		hb, _ := idx.Search(req.Context(), "bravo", 50)
		ha, _ := idx.Search(req.Context(), "alpha", 50)
		return len(hb) > 0 && len(ha) == 0
	}, 3*time.Second, 20*time.Millisecond)
}

func TestPutSettings_EnabledChangeStillRestart(t *testing.T) {
	d := newSettingsDeps(t)
	e := echo.New()
	RegisterSettings(e, d)
	req := httptest.NewRequest(http.MethodPut, "/v1/search/settings", strings.NewReader(`{"fileindex_enabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, true, resp["restart_required"], "enabled toggle still needs restart")
}

func TestGetSettings_RootsInRuntimeFields(t *testing.T) {
	e := echo.New()
	RegisterSettings(e, newSettingsDeps(t))
	req := httptest.NewRequest(http.MethodGet, "/v1/search/settings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		RuntimeFields []string `json:"runtime_fields"`
		RestartFields []string `json:"restart_fields"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Contains(t, resp.RuntimeFields, "fileindex_roots")
	require.NotContains(t, resp.RestartFields, "fileindex_roots")
	require.Contains(t, resp.RestartFields, "fileindex_enabled")
	require.Contains(t, resp.RestartFields, "fileindex_scan_interval_h")
}
