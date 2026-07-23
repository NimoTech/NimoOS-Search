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

// 授权源已从 Wiki 切到核心(NimoOS 主服务,见 Task 8)。Gateway 拒绝所有
// /_internal/ 路径(NimoOS-Gateway e2c9b9c),所以 root 授权查询依旧要经由
// discovery 文件直连核心。
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
