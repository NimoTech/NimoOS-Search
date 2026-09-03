package v1

import (
	"errors"
	"net/http"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
)

// RegisterAgent wires the agent tool endpoints onto the Echo instance.
func RegisterAgent(e *echo.Echo, d *Deps) {
	e.GET("/v1/search/agent/tools", getAgentTools(d))
	e.POST("/v1/search/agent/tool", postAgentTool(d))
	e.GET("/v1/search/agent/filters-schema", getAgentFiltersSchema(d))
}

// getAgentTools returns the OpenAI function-calling schema for agent tool use.
func getAgentTools(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, d.Tools.ToolsSchema())
	}
}

type postAgentToolBody struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// postAgentTool dispatches a single agent tool invocation. Requires a user ID
// (injected by InjectUserID middleware) for root-scope enforcement.
func postAgentTool(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		var body postAgentToolBody
		if err := c.Bind(&body); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		if body.Name == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "name is required")
		}
		uid, _ := c.Get(CtxUserIDKey).(string)
		if uid == "" {
			// Header is injected by the Gateway after JWT verification (or the
			// localhost exemption). Absent here means a wiring bug upstream, not
			// an end-user state — fail loudly rather than silently returning no
			// hits. (Empty *roots* for a known user is still a 200 + warning,
			// produced by ApplyScope below.) See spec 2026-05-29.
			return echo.NewHTTPError(http.StatusBadRequest, "X-NimoOS-User-ID required")
		}
		var allowedRoots, allowedRootPaths []string
		if d.NimoOS != nil {
			var err error
			allowedRoots, err = d.NimoOS.SearchRoots(c.Request().Context(), uid)
			if err != nil {
				return echo.NewHTTPError(http.StatusServiceUnavailable,
					"unable to determine user roots: "+err.Error())
			}
			// Paths scope the filename index. A core too old to report them is
			// not an outage: leave nil so the filenames source fails closed with
			// a warning while semantic/images/notes still answer.
			allowedRootPaths, err = d.NimoOS.SearchRootPaths(c.Request().Context(), uid)
			if err != nil && !errors.Is(err, service.ErrRootPathsUnavailable) {
				return echo.NewHTTPError(http.StatusServiceUnavailable,
					"unable to determine user root paths: "+err.Error())
			}
			if err != nil {
				allowedRootPaths = nil
			}
		}
		if body.Arguments == nil {
			body.Arguments = map[string]any{}
		}
		body.Arguments["__user_id"] = uid
		result, err := d.Tools.Invoke(c.Request().Context(), body.Name, body.Arguments, allowedRoots, allowedRootPaths)
		if errors.Is(err, service.ErrFileNotInScope) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, result)
	}
}

// getAgentFiltersSchema returns the JSON schema for the filters parameter
// accepted by the nimoos_search tool.
func getAgentFiltersSchema(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, d.Tools.FiltersSchema())
	}
}
