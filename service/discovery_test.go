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

func TestNimoOSClientRetriesViaDiscoveryOnTransportError(t *testing.T) {
	// live server = "重启后的新核心(NimoOS 主服务)"。授权源已从 Wiki 切到核心
	// (见 Task 8),但 doWithRediscover 的重试语义对任何走 BaseURLSource 的
	// client 都一样,这里改用 NimoOSClient 继续覆盖该行为。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"root_ids":[]}`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	f := filepath.Join(dir, "nimoos.url")
	// 先指向一个必然拒连的端口(占用后立刻关掉的地址)
	deadAddr := "http://127.0.0.1:1"
	require.NoError(t, os.WriteFile(f, []byte(deadAddr), 0644))
	c := NewNimoOSClient(NewBaseURLSource(f, deadAddr), time.Minute)

	// Precondition: dial the dead address and prime the BaseURLSource's
	// cache with it (both the initial attempt and the Refresh()-retry
	// re-read the same dead-address file, so this must fail). If this
	// assertion ever passes, the retry path below is not being exercised
	// and the test has gone vacuous again.
	_, err := c.SearchRoots(context.Background(), "u1")
	require.Error(t, err, "request against the dead address must fail before the file is hot-swapped")

	// Now hot-swap the discovery file to the live server — same as a peer
	// restarting on a new random port. The client's cached base URL still
	// points at the dead address, so the first attempt below must fail at
	// transport level and only succeed via src.Refresh() + retry.
	require.NoError(t, os.WriteFile(f, []byte(srv.URL), 0644))
	_, err = c.SearchRoots(context.Background(), "u1")
	require.NoError(t, err, "transport error must trigger discovery refresh + one retry")
}
