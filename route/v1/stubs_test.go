package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestHybridStub503(t *testing.T) {
	e := echo.New()
	RegisterStubs(e)
	req := httptest.NewRequest(http.MethodPost, "/v1/search/hybrid", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestThumbStub404(t *testing.T) {
	e := echo.New()
	RegisterStubs(e)
	req := httptest.NewRequest(http.MethodGet, "/v1/search/thumb/abc", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
