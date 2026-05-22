package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWikiUserRoots(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/v1/wiki/_internal/user-roots", r.URL.Path)
		require.Equal(t, "u1", r.URL.Query().Get("user_id"))
		w.Write([]byte(`{"root_ids":["r1","r2"]}`))
	}))
	defer srv.Close()

	c := NewWikiClient(srv.URL, 5, 60*time.Second)
	out, err := c.UserRoots(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r2"}, out)
	require.Equal(t, 1, calls)

	// 2nd call within cache TTL → no new HTTP hit
	out2, err := c.UserRoots(context.Background(), "u1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1", "r2"}, out2)
	require.Equal(t, 1, calls)
}

func TestWikiUserRoots_CacheExpiry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"root_ids":["r1"]}`))
	}))
	defer srv.Close()
	c := NewWikiClient(srv.URL, 5, 10*time.Millisecond)
	_, _ = c.UserRoots(context.Background(), "u1")
	time.Sleep(20 * time.Millisecond)
	_, _ = c.UserRoots(context.Background(), "u1")
	require.Equal(t, 2, calls)
}
