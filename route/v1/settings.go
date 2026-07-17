package v1

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
)

const inotifyRecommended = 524288

func RegisterSettings(e *echo.Echo, d *Deps) {
	e.GET("/v1/search/settings", getSettings(d))
	e.PUT("/v1/search/settings", putSettings(d))
	e.POST("/v1/search/fileindex/rescan", postRescan(d))
	e.GET("/v1/search/fileindex/status", getFileindexStatus(d))
}

var runtimeFields = []string{"default_sources", "semantic_top_k", "filename_top_k", "image_top_k", "notes_top_k", "max_total_results", "fileindex_roots"}
var restartFields = []string{"fileindex_enabled", "fileindex_scan_interval_h"}

func getSettings(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]any{
			"settings":       d.Settings.Get(),
			"runtime_fields": runtimeFields,
			"restart_fields": restartFields,
		})
	}
}

func putSettings(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		var patch service.SearchSettingsPatch
		if err := c.Bind(&patch); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid JSON: "+err.Error())
		}
		cur := d.Settings.Get()
		merged := cur.ApplyPatch(patch)
		if err := merged.Validate(); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		if err := d.Settings.Put(merged); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to persist settings")
		}
		// fileindex_roots is hot: when it actually changes, reload the index live.
		if patch.FileIndexRoots != nil && !strSliceEq(*patch.FileIndexRoots, cur.FileIndexRoots) && d.FileIndex != nil {
			d.FileIndex.ReloadRoots(merged.FileIndexRoots)
		}
		restartRequired := (patch.FileIndexEnabled != nil && *patch.FileIndexEnabled != cur.FileIndexEnabled) ||
			(patch.FileIndexScanIntervalH != nil && *patch.FileIndexScanIntervalH != cur.FileIndexScanIntervalH)
		return c.JSON(http.StatusOK, map[string]any{
			"settings":         d.Settings.Get(),
			"restart_required": restartRequired,
		})
	}
}

func postRescan(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		if d.FileIndex == nil || d.FileIndex.Index == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "file index disabled")
		}
		d.FileIndex.RescanActiveRoots()
		return c.JSON(http.StatusOK, map[string]any{"started": true})
	}
}

func getFileindexStatus(d *Deps) echo.HandlerFunc {
	return func(c echo.Context) error {
		rep := d.FileIndex.Report() // nil-safe → disabled
		out := map[string]any{
			"status":         rep.Status,
			"indexed_count":  rep.IndexedCount,
			"watch_degraded": rep.WatchDegraded,
		}
		if n, ok := readInotifyMax(); ok {
			out["inotify"] = map[string]any{
				"max_user_watches": n,
				"recommended":      inotifyRecommended,
				"raise_cmd":        "sudo sysctl -w fs.inotify.max_user_watches=" + strconv.Itoa(inotifyRecommended),
			}
		}
		return c.JSON(http.StatusOK, out)
	}
}

// readInotifyMax reads the kernel limit; returns (0,false) on any error
// (missing file / permission / non-Linux) so status never 500s.
func readInotifyMax() (int, bool) {
	b, err := os.ReadFile("/proc/sys/fs/inotify/max_user_watches")
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return n, true
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
