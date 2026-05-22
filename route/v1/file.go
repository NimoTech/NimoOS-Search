package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
)

func RegisterFile(e *echo.Echo, d *Deps) {
	e.GET("/v1/search/file", getSearchFile(d))
}

func getSearchFile(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		fileID := c.QueryParam("file_id")
		if fileID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "file_id is required")
		}
		offset, _ := strconv.Atoi(c.QueryParam("offset"))
		limit, _ := strconv.Atoi(c.QueryParam("limit"))
		if limit <= 0 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
		uid, _ := c.Get(CtxUserIDKey).(string)
		allowed, err := d.Wiki.UserRoots(c.Request().Context(), uid)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
		}
		resp, err := d.Authz.GetFileChunks(c.Request().Context(), fileID, allowed, offset, limit)
		if errors.Is(err, service.ErrFileNotInScope) {
			return echo.NewHTTPError(http.StatusNotFound, "file not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, resp)
	}
}
