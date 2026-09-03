package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/require"
)

func newJWTTestEcho(t *testing.T) (*echo.Echo, *string) {
	t.Helper()
	e := echo.New()
	e.Use(echo_middleware.JWTWithConfig(JWTConfig(t.TempDir()))) // empty runtime dir: no JWKS → any token invalid
	e.Use(InjectUserID)
	var seen string
	e.GET("/x", func(c echo.Context) error {
		seen, _ = c.Get(CtxUserIDKey).(string)
		return c.NoContent(http.StatusNoContent)
	})
	return e, &seen
}

func TestJWT_ExternalWithoutToken_401(t *testing.T) {
	e, _ := newJWTTestEcho(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.5:33333"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWT_ExternalForgedUserHeader_401(t *testing.T) {
	e, seen := newJWTTestEcho(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "10.0.0.5:33333"
	req.Header.Set("X-NimoOS-User-ID", "1") // forged: must not grant identity without a JWT
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Empty(t, *seen, "handler must not run for an unauthenticated external request")
}

func TestJWT_ProxiedExternal_XForwardedFor_401(t *testing.T) {
	// Gateway forwards from loopback but sets X-Forwarded-For to the real
	// client; RealIP() must see the external address and demand a JWT.
	e, _ := newJWTTestEcho(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-Forwarded-For", "192.168.1.50")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestJWT_LocalhostTrustsUserHeader(t *testing.T) {
	e, seen := newJWTTestEcho(t)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	req.Header.Set("X-NimoOS-User-ID", "42")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "42", *seen)
}

func TestLocalhostOnly_RejectsExternal(t *testing.T) {
	e := echo.New()
	e.GET("/i", LocalhostOnly(func(c echo.Context) error { return c.NoContent(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodGet, "/i", nil)
	req.RemoteAddr = "10.0.0.5:33333"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}
