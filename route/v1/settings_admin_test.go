package v1

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// withUserService points Deps at a fake UserService answering with role.
func withUserService(t *testing.T, d *Deps, role string) {
	t.Helper()
	srv, _ := fakeUserService(t, role)
	d.UserServiceURL = func() (string, error) { return srv.URL, nil }
}

func doPut(e *echo.Echo, path, body, auth string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestPutSettings_NoToken_401(t *testing.T) {
	e := echo.New()
	d := newSettingsDeps(t)
	withUserService(t, d, "admin")
	RegisterSettings(e, d)
	rec := doPut(e, "/v1/search/settings", `{"semantic_top_k":9}`, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, 5, d.Settings.Get().SemanticTopK, "settings must be untouched")
}

func TestPutSettings_NonAdmin_403(t *testing.T) {
	e := echo.New()
	d := newSettingsDeps(t)
	withUserService(t, d, "user")
	RegisterSettings(e, d)
	rec := doPut(e, "/v1/search/settings", `{"semantic_top_k":9}`, "Bearer tok")
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, 5, d.Settings.Get().SemanticTopK, "settings must be untouched")
}

func TestRescan_NonAdmin_403(t *testing.T) {
	e := echo.New()
	d := newSettingsDeps(t)
	withUserService(t, d, "user")
	RegisterSettings(e, d)
	req := httptest.NewRequest(http.MethodPost, "/v1/search/fileindex/rescan", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGetSettings_StillOpenToAnyUser(t *testing.T) {
	// Read-only view stays available to any authenticated user (the UI's
	// search page reads it); only writes are admin-gated.
	e := echo.New()
	d := newSettingsDeps(t)
	RegisterSettings(e, d)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/settings", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestInternal_RejectsExternalCaller(t *testing.T) {
	e := echo.New()
	RegisterInternal(e, &Deps{})
	req := httptest.NewRequest(http.MethodGet, "/v1/search/_internal/stats", nil)
	req.RemoteAddr = "10.0.0.5:33333"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
