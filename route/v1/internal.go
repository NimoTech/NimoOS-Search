package v1

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

func Healthz(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// RegisterInternal wires the localhost-only operational endpoints. NOT
// registered to Gateway.
func RegisterInternal(e *echo.Echo, d *Deps) {
	g := e.Group("/v1/search/_internal")
	g.GET("/health", getInternalHealth(d))
	g.GET("/stats", getInternalStats(d))
	g.POST("/warm", postInternalWarm(d))
}

func getInternalHealth(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Wiring lands in T22; for now report degraded if Search service nil
		if d.Search == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]any{
				"status": "degraded", "detail": "search service not wired",
			})
		}
		return c.JSON(http.StatusOK, map[string]any{"status": "ok"})
	}
}

func getInternalStats(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Real KPIs come from the eventbus rolling stats (T25). MVP stats
		// endpoint reports a static snapshot of zero values.
		return c.JSON(http.StatusOK, map[string]any{
			"cache":            map[string]any{"hit_rate": 0.0},
			"rerank_fallback":  map[string]any{"rate": 0.0},
			"query_latency_p95": map[string]any{"with_rerank_ms": 0, "without_rerank_ms": 0},
			"tool_calls_per_minute": 0,
		})
	}
}

func postInternalWarm(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Run a one-shot internal query to trigger BGE-M3 load on Parser side.
		// MVP: just return ok; the real warm-up is invoked from main.go's
		// startup hook (T22) so by the time anything calls /warm BGE-M3 is
		// likely already loaded.
		return c.JSON(http.StatusOK, map[string]any{"warmed": true})
	}
}
