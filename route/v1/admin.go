package v1

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
)

const adminRole = "admin"

// ErrNoUserService is returned by a URL resolver when UserService cannot be
// discovered; AdminOnly maps it to 503.
var ErrNoUserService = errors.New("user-service unavailable")

// AdminOnly gates a handler to admin users. It re-uses the caller's own
// Authorization header to ask UserService (GET /v1/users/current) for the
// role, the same way NimoOS-Terminal guards its settings. A request without a
// token is rejected with 401 even on the localhost JWT-exempt path: the
// settings/rescan endpoints have no legitimate anonymous caller.
func AdminOnly(userServiceURL func() (string, error)) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			auth := strings.TrimSpace(c.Request().Header.Get(echo.HeaderAuthorization))
			if auth == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "admin token required")
			}
			if userServiceURL == nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "user-service unavailable")
			}
			base, err := userServiceURL()
			if err != nil || base == "" {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "user-service unavailable")
			}
			ok, err := isAdmin(c.Request().Context(), base, auth)
			if err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable, "admin check failed")
			}
			if !ok {
				return echo.NewHTTPError(http.StatusForbidden, "admin only")
			}
			return next(c)
		}
	}
}

var adminHTTP = &http.Client{Timeout: 5 * time.Second}

func isAdmin(ctx context.Context, userServiceBase, authHeader string) (bool, error) {
	// UserService's JWT extractor accepts both bare and "Bearer "-prefixed
	// tokens, so the header is forwarded verbatim.
	url := strings.TrimSuffix(userServiceBase, "/") + "/v1/users/current"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set(echo.HeaderAuthorization, authHeader)
	resp, err := adminHTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return false, nil // invalid/expired token → not admin, not an outage
	}
	if resp.StatusCode != http.StatusOK {
		return false, errors.New("user-service status " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, err
	}
	var parsed struct {
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, err
	}
	return parsed.Data.Role == adminRole, nil
}
