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

// The gateway refuses every /_internal/ path (NimoOS-Gateway e2c9b9c), so
// user-roots must go direct to the Wiki service via its discovery file.
func TestNewWikiClientReadsDiscoveryFile(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"root_ids":["r1"]}`))
	}))
	defer srv.Close()

	p := filepath.Join(t.TempDir(), "wiki.url")
	require.NoError(t, os.WriteFile(p, []byte(srv.URL+"\n"), 0o644))

	cfg := config.Config{WikiDiscoveryPath: p, WikiTimeoutSec: 5, UserRootsCacheTTLSec: 60}
	wc, err := newWikiClient(cfg)
	require.NoError(t, err)

	roots, err := wc.UserRoots(context.Background(), "1")
	require.NoError(t, err)
	require.Equal(t, []string{"r1"}, roots)
	require.Equal(t, "/v1/wiki/_internal/user-roots", gotPath)
}
