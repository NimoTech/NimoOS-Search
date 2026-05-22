package v1

import (
	"github.com/labstack/echo/v4"
)

// NewRouter wires all v1 routes onto the provided Echo instance.
// Later tasks add /v1/search/* routers; T1 only registers /healthz.
func NewRouter(e *echo.Echo) {
	e.GET("/healthz", healthz)
}
