package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPhotosClient_SmartSearch_MapsAssets(t *testing.T) {
	var gotUser string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/photos/search/smart", r.URL.Path)
		gotUser = r.Header.Get("X-NimoOS-User-ID")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`[{"id":"a1","originalName":"beach.jpg","filePath":"/DATA/Gallery/beach.jpg","matchScore":0.83,"takenAt":"2025-07-01T00:00:00Z"}]`))
	}))
	defer srv.Close()

	c := NewPhotosClient(NewBaseURLSource("", srv.URL), 5)
	hits, err := c.SmartSearch(context.Background(), "beach", 7, "42")
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "a1", hits[0].AssetID)
	require.Equal(t, "beach.jpg", hits[0].Name)
	require.Equal(t, "/DATA/Gallery/beach.jpg", hits[0].Path)
	require.InDelta(t, 0.83, hits[0].Score, 1e-6)
	require.Equal(t, "/v1/photos/assets/a1/thumbnail?size=small", hits[0].ThumbnailURL)
	require.Equal(t, "42", gotUser, "user id is passed through for future per-user support")
	require.EqualValues(t, 7, gotBody["limit"])
}

func TestPhotosClient_SmartSearch_ErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewPhotosClient(NewBaseURLSource("", srv.URL), 5)
	_, err := c.SmartSearch(context.Background(), "x", 5, "")
	require.Error(t, err)
}
