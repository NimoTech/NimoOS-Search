package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func RegisterVisual(e *echo.Echo, d *Deps) {
	e.POST("/v1/search/visual", postVisual(d))
}

type postVisualBody struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k,omitempty"`
}

// postVisual proxies image search to NimoOS-Photos. Returns 503 if Photos was
// not discovered at startup.
func postVisual(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		if d.Photos == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "photos service unavailable")
		}
		var body postVisualBody
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		if body.Query == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "query is required")
		}
		topK := body.TopK
		if topK <= 0 || topK > 100 {
			topK = 20
		}
		uid, _ := c.Get(CtxUserIDKey).(string)
		hits, err := d.Photos.SmartSearch(c.Request().Context(), body.Query, topK, uid)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadGateway, "photos search failed: "+err.Error())
		}
		return c.JSON(http.StatusOK, map[string]any{"images": hits})
	}
}
