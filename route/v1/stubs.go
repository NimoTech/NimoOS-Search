package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// RegisterStubs wires MVP placeholder endpoints. NOT registered to Gateway
// (see main.go Task 23) so external clients don't see them; they exist so the
// OpenAPI surface and internal callers get a consistent 503/404 instead of
// a 404 from Echo's default routing.
func RegisterStubs(e *echo.Echo) {
	e.POST("/v1/search/visual", stubVisual)
	e.POST("/v1/search/hybrid", stubHybrid)
	e.GET("/v1/search/thumb/:file_id", stubThumb)
}

func stubVisual(c echo.Context) error {
	return c.JSON(http.StatusServiceUnavailable, map[string]any{
		"error":  "visual pipeline not yet available",
		"detail": "depends on NimoOS-Parser image pipeline",
	})
}

func stubHybrid(c echo.Context) error {
	return c.JSON(http.StatusServiceUnavailable, map[string]any{
		"error":  "hybrid pipeline not yet available",
		"detail": "depends on NimoOS-Parser image pipeline",
	})
}

func stubThumb(c echo.Context) error {
	return c.JSON(http.StatusNotFound, map[string]any{
		"error":  "thumbnail service not yet implemented",
		"detail": "see design spec §9.2",
	})
}
