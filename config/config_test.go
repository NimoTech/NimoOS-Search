package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load("/nonexistent.conf")
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", c.BindHost)
	require.Equal(t, 20, c.DefaultTopK)
	require.Equal(t, 100, c.MaxTopK)
	require.Equal(t, 1000, c.EmbedCacheSize)
	require.Equal(t, 300, c.EmbedCacheTTLSec)
	require.Equal(t, "http://127.0.0.1:6333", c.QdrantURL)
	require.Equal(t, 6334, c.QdrantGRPCPort)
}

func TestLoadFromINI(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, "search.conf")
	require.NoError(t, os.WriteFile(conf, []byte(`
[search]
DefaultTopK = 10
EmbedCacheSize = 500
QdrantUrl = http://qdrant.local:6333
`), 0644))
	c, err := Load(conf)
	require.NoError(t, err)
	require.Equal(t, 10, c.DefaultTopK)
	require.Equal(t, 500, c.EmbedCacheSize)
	require.Equal(t, "http://qdrant.local:6333", c.QdrantURL)
}

func TestEnvOverridesINI(t *testing.T) {
	t.Setenv("SEARCH_DEFAULT_TOP_K", "5")
	c, err := Load("/nonexistent.conf")
	require.NoError(t, err)
	require.Equal(t, 5, c.DefaultTopK)
}

func TestDefaults_FileIndexAndAggregate(t *testing.T) {
	c := defaults()
	require.True(t, c.FileIndexEnabled)
	require.Equal(t, []string{"/DATA", "/mnt", "/media"}, c.FileIndexRoots)
	require.Equal(t, "/var/lib/nimoos/db/search.db", c.FileIndexDBPath)
	require.Equal(t, 6, c.FileIndexScanIntervalH)
	require.Equal(t, 1, c.FileIndexDegradedScanIntervalH)
	require.Equal(t, "/var/run/nimoos/photos.url", c.PhotosDiscoveryPath)
	require.Equal(t, 5, c.AggSemanticTopK)
	require.Equal(t, 5, c.AggFilenameTopK)
	require.Equal(t, 5, c.AggImageTopK)
	require.Equal(t, 15, c.AggMaxTotalResults)
	require.Equal(t, "/var/lib/nimoos/search-settings.json", c.SettingsPath)
}
