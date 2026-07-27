package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNimoOSClient_SearchRoots(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/nimoos/search-roots", r.URL.Path)
		require.Equal(t, "u1", r.URL.Query().Get("user_id"))
		w.Write([]byte(`{"root_ids":["aabb","photos"]}`))
	}))
	defer srv.Close()

	c := NewNimoOSClient(NewBaseURLSource("", srv.URL), time.Minute)
	got, err := c.SearchRoots(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"aabb", "photos"}, got)
	require.Equal(t, 1, calls)

	// 第二次命中缓存,不再打后端
	_, err = c.SearchRoots(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestNimoOSClient_SearchRoots_CacheExpiry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"root_ids":["r1"]}`))
	}))
	defer srv.Close()

	c := NewNimoOSClient(NewBaseURLSource("", srv.URL), 10*time.Millisecond)
	_, _ = c.SearchRoots(context.Background(), "u1")
	time.Sleep(20 * time.Millisecond)
	_, _ = c.SearchRoots(context.Background(), "u1")
	require.Equal(t, 2, calls)
}
