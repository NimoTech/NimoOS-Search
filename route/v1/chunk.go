package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
)

func RegisterChunk(e *echo.Echo, d *Deps) {
	e.GET("/v1/search/chunk", getSearchChunk(d))
}

func getSearchChunk(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		fileID := c.QueryParam("file_id")
		kind := c.QueryParam("kind")
		chunkNoStr := c.QueryParam("chunk_no")
		if fileID == "" || kind == "" || chunkNoStr == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "file_id, kind, chunk_no required")
		}
		chunkNo, err := strconv.Atoi(chunkNoStr)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "chunk_no must be int")
		}
		window, _ := strconv.Atoi(c.QueryParam("window"))
		if window <= 0 {
			window = 2
		}
		if window > 5 {
			window = 5
		}
		uid, _ := c.Get(CtxUserIDKey).(string)
		allowed, err := d.NimoOS.SearchRoots(c.Request().Context(), uid)
		if err != nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, err.Error())
		}
		resp, err := d.Authz.GetChunkWindow(c.Request().Context(), fileID, kind, chunkNo, window, allowed)
		if errors.Is(err, service.ErrFileNotInScope) {
			return echo.NewHTTPError(http.StatusNotFound, "not found")
		}
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, resp)
	}
}
