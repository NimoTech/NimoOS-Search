package v1

import (
	"net/http"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
)

type postSearchTextBody struct {
	Query   string           `json:"query"`
	Filters *service.Filters `json:"filters,omitempty"`
	TopK    int              `json:"top_k,omitempty"`
	Rerank  *bool            `json:"rerank,omitempty"`
}

func RegisterText(e *echo.Echo, d *Deps) {
	e.POST("/v1/search/text", postSearchText(d))
}

func postSearchText(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body postSearchTextBody
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		if body.Query == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "query is required")
		}
		uid, _ := c.Get(CtxUserIDKey).(string)
		allowed, err := d.Wiki.UserRoots(c.Request().Context(), uid)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable,
				"unable to determine user roots: "+err.Error())
		}
		filters, warn := service.ApplyScope(body.Filters, allowed)
		if warn == "no_accessible_roots" {
			return c.JSON(http.StatusOK, service.SearchResponse{
				Hits: []service.Hit{}, Warnings: []string{warn},
			})
		}
		rerank := true
		if body.Rerank != nil {
			rerank = *body.Rerank
		}
		resp, err := d.Search.SearchText(c.Request().Context(), service.SearchRequest{
			Query: body.Query, Filters: filters, TopK: body.TopK, Rerank: rerank,
		})
		if err != nil {
			switch {
			case errorsIs(err, service.ErrEmbedderUnavailable):
				return c.JSON(http.StatusServiceUnavailable, map[string]any{
					"error": "embedder unavailable", "retry_after": 5,
				})
			default:
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
		}
		return c.JSON(http.StatusOK, resp)
	}
}

// errorsIs is a tiny helper to avoid pulling errors.Is everywhere in handlers
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type wrap interface{ Unwrap() error }
		if w, ok := err.(wrap); ok {
			err = w.Unwrap()
			continue
		}
		return false
	}
	return false
}
