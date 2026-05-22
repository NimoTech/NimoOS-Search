package v1

import (
	"github.com/labstack/echo/v4"
)

// CtxUserIDKey is the Echo context key under which the resolved
// X-NimoOS-User-ID lives. Set by InjectUserID middleware.
const CtxUserIDKey = "nimoos_user_id"

// InjectUserID reads X-NimoOS-User-ID from the request (set by Gateway after
// JWT verification, or by localhost callers that the Gateway exempts) and
// stashes it in the Echo context. Endpoints that need authorization (text,
// file, chunk, agent/tool) check c.Get(CtxUserIDKey) and reject empty.
func InjectUserID(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		uid := c.Request().Header.Get("X-NimoOS-User-ID")
		c.Set(CtxUserIDKey, uid)
		return next(c)
	}
}
