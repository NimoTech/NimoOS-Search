package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBaseURLSourceRefreshPicksUpNewAddress(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "wiki.url")
	require.NoError(t, os.WriteFile(f, []byte("http://127.0.0.1:1111"), 0644))

	s := NewBaseURLSource(f, "http://fallback")
	require.Equal(t, "http://127.0.0.1:1111", s.Get())

	require.NoError(t, os.WriteFile(f, []byte("http://127.0.0.1:2222"), 0644))
	require.Equal(t, "http://127.0.0.1:1111", s.Get(), "Get stays cached")
	require.Equal(t, "http://127.0.0.1:2222", s.Refresh())
	require.Equal(t, "http://127.0.0.1:2222", s.Get())
}

func TestWikiClientRetriesViaDiscoveryOnTransportError(t *testing.T) {
	// live server = "重启后的新 wiki"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"root_ids":[]}`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	f := filepath.Join(dir, "wiki.url")
	// 先指向一个必然拒连的端口(占用后立刻关掉的地址),再热切到 live server
	require.NoError(t, os.WriteFile(f, []byte("http://127.0.0.1:1"), 0644))
	c := NewWikiClient(NewBaseURLSource(f, "http://127.0.0.1:1"), 2, time.Minute)

	require.NoError(t, os.WriteFile(f, []byte(srv.URL), 0644))
	_, err := c.UserRoots(context.Background(), "u1") // 实际方法签名以 wiki_client.go 为准
	require.NoError(t, err, "transport error must trigger discovery refresh + one retry")
}
