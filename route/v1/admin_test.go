package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// fakeUserService answers GET /v1/users/current with the given role and
// records the Authorization header it received.
func fakeUserService(t *testing.T, role string) (*httptest.Server, *string) {
	t.Helper()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/users/current", r.URL.Path)
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"data":{"id":1,"role":"` + role + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotAuth
}

func adminTestEcho(usURL string) *echo.Echo {
	e := echo.New()
	e.PUT("/s", AdminOnly(func() (string, error) { return usURL, nil })(func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	}))
	return e
}

func TestAdminOnly_AdminPasses(t *testing.T) {
	srv, gotAuth := fakeUserService(t, "admin")
	e := adminTestEcho(srv.URL)
	req := httptest.NewRequest(http.MethodPut, "/s", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "Bearer tok", *gotAuth, "must forward the caller's token to UserService")
}

func TestAdminOnly_NonAdmin_403(t *testing.T) {
	srv, _ := fakeUserService(t, "user")
	e := adminTestEcho(srv.URL)
	req := httptest.NewRequest(http.MethodPut, "/s", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAdminOnly_NoToken_401(t *testing.T) {
	// Localhost callers skip JWT and normally carry no Authorization header;
	// admin endpoints must still demand a token so the role can be checked.
	srv, _ := fakeUserService(t, "admin")
	e := adminTestEcho(srv.URL)
	req := httptest.NewRequest(http.MethodPut, "/s", nil)
	req.RemoteAddr = "127.0.0.1:40000"
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAdminOnly_UserServiceDown_503(t *testing.T) {
	e := adminTestEcho("http://127.0.0.1:1") // nothing listens
	req := httptest.NewRequest(http.MethodPut, "/s", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
