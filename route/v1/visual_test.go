package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

type fakeImages struct{ hits []service.ImageHit }

func (f fakeImages) SmartSearch(context.Context, string, int, string) ([]service.ImageHit, error) {
	return f.hits, nil
}

func TestVisualEndpoint_ReturnsImages(t *testing.T) {
	e := echo.New()
	e.Use(InjectUserID)
	d := &Deps{Photos: fakeImages{hits: []service.ImageHit{{AssetID: "a1", Name: "p.jpg"}}}}
	RegisterVisual(e, d)

	req := httptest.NewRequest(http.MethodPost, "/v1/search/visual", strings.NewReader(`{"query":"cat","top_k":3}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "a1")
}

func TestVisualEndpoint_NoPhotos503(t *testing.T) {
	e := echo.New()
	e.Use(InjectUserID)
	RegisterVisual(e, &Deps{})
	req := httptest.NewRequest(http.MethodPost, "/v1/search/visual", strings.NewReader(`{"query":"cat"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
