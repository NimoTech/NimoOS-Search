package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NimoTech/NimoOS-Search/config"
	"github.com/stretchr/testify/require"
)

// The authorization source moved from Wiki to core (the main NimoOS
// service, see Task 8). The Gateway rejects all /_internal/ paths
// (NimoOS-Gateway e2c9b9c), so root authorization queries still have to go
// through the discovery file directly to core.
func TestNewNimoOSClientReadsDiscoveryFile(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"root_ids":["r1"]}`))
	}))
	defer srv.Close()

	p := filepath.Join(t.TempDir(), "nimoos.url")
	require.NoError(t, os.WriteFile(p, []byte(srv.URL+"\n"), 0o644))

	cfg := config.Config{NimoOSDiscoveryPath: p, UserRootsCacheTTLSec: 60}
	nc, err := newNimoOSClient(cfg)
	require.NoError(t, err)

	roots, err := nc.SearchRoots(context.Background(), "1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1"}, roots)
	require.Equal(t, "/v1/nimoos/search-roots", gotPath)
}
