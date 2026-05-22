package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestUserIDExtraction_HeaderInjected(t *testing.T) {
	e := echo.New()
	e.Use(InjectUserID)
	e.GET("/probe", func(c echo.Context) error {
		uid, _ := c.Get(CtxUserIDKey).(string)
		return c.String(http.StatusOK, uid)
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-NimoOS-User-ID", "u123")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "u123", rec.Body.String())
}

func TestUserIDExtraction_MissingHeader(t *testing.T) {
	e := echo.New()
	e.Use(InjectUserID)
	e.GET("/probe", func(c echo.Context) error {
		uid, _ := c.Get(CtxUserIDKey).(string)
		return c.String(http.StatusOK, uid)
	})
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "", rec.Body.String())
}
