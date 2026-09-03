package v1

import (
	"crypto/ecdsa"
	"net/http"
	"strconv"
	"strings"

	"github.com/NimoTech/NimoOS-Common/external"
	"github.com/NimoTech/NimoOS-Common/utils/jwt"
	"github.com/labstack/echo/v4"
	echo_middleware "github.com/labstack/echo/v4/middleware"
)

// isLocalhostRealIP reports whether the *originating* caller is on this host.
// echo's RealIP() prefers X-Forwarded-For / X-Real-IP over RemoteAddr. The
// Gateway scrubs client-supplied X-Forwarded-For and re-adds the real client
// address (NimoOS-Gateway/route/gateway_route.go rewriteRequestSourceIP), so
// proxied external traffic resolves to the external client, and only genuine
// in-host callers (the AI agent, CLI, internal workers) take the localhost path.
func isLocalhostRealIP(c echo.Context) bool {
	host := c.RealIP()
	return host == "127.0.0.1" || host == "::1" || host == "localhost"
}

// JWTConfig returns echo's JWT middleware configured the same way as the
// other NimoOS services (see NimoOS-Wiki/route/middleware.go): external
// requests must carry a valid UserService JWT (bare or "Bearer "-prefixed);
// localhost callers are exempt and their X-NimoOS-User-ID header is trusted.
// On the JWT path the header is overwritten from the verified claims, so a
// client can never forge an identity by setting the header itself.
func JWTConfig(runtimePath string) echo_middleware.JWTConfig {
	return echo_middleware.JWTConfig{
		Skipper: func(c echo.Context) bool {
			return isLocalhostRealIP(c)
		},
		ParseTokenFunc: func(token string, c echo.Context) (interface{}, error) {
			valid, claims, err := jwt.Validate(token, func() (*ecdsa.PublicKey, error) {
				return external.GetPublicKey(runtimePath)
			})
			if err != nil || !valid {
				return nil, echo.ErrUnauthorized
			}
			c.Request().Header.Set("X-NimoOS-User-ID", strconv.Itoa(claims.ID))
			c.Request().Header.Set("X-NimoOS-User-Name", claims.Username)
			return claims, nil
		},
		TokenLookupFuncs: []echo_middleware.ValuesExtractor{
			func(c echo.Context) ([]string, error) {
				auth := c.Request().Header.Get(echo.HeaderAuthorization)
				return []string{strings.TrimPrefix(auth, "Bearer ")}, nil
			},
		},
	}
}

// LocalhostOnly rejects any request not originating on this host. Used as
// defense in depth for /v1/search/_internal/* in addition to the Gateway's
// refusal to proxy any /_internal/ path.
func LocalhostOnly(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if !isLocalhostRealIP(c) {
			return echo.NewHTTPError(http.StatusForbidden, "internal endpoint")
		}
		return next(c)
	}
}
