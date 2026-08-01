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
	// live server = "the restarted new core (main NimoOS service)". The
	// authorization source moved from Wiki to core (see Task 8), but
	// doWithRediscover's retry semantics are the same for any client going
	// through BaseURLSource, so we switch to NimoOSClient here to keep
	// covering that behavior.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"root_ids":[]}`))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	f := filepath.Join(dir, "nimoos.url")
	// First point at a port that's guaranteed to refuse connections (an
	// address that was bound and immediately closed)
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
