package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestInternalHealth(t *testing.T) {
	deps := &Deps{} // probes will all fail with nil clients
	e := echo.New()
	RegisterInternal(e, deps)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/_internal/health", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.True(t, rec.Code == http.StatusOK || rec.Code == http.StatusServiceUnavailable)
}

func TestInternalStats(t *testing.T) {
	deps := &Deps{}
	e := echo.New()
	RegisterInternal(e, deps)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/_internal/stats", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}
