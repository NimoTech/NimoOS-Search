package service

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func defaultsForTest() SearchSettings {
	return SearchSettings{
		DefaultSources: []string{"semantic", "filenames", "images"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, NotesTopK: 5, MaxTotalResults: 15,
		FileIndexEnabled: true, FileIndexRoots: []string{"/DATA"}, FileIndexScanIntervalH: 6,
	}
}

// defaultsWithNotes mirrors main.go's real (post-notes) defaults: all four
// sources, stamped at the current schema version.
func defaultsWithNotes() SearchSettings {
	return SearchSettings{
		SchemaVersion:  SettingsSchemaVersion,
		DefaultSources: []string{"semantic", "filenames", "images", "notes"},
		SemanticTopK:   5, FilenameTopK: 5, ImageTopK: 5, NotesTopK: 5, MaxTotalResults: 15,
		FileIndexEnabled: true, FileIndexRoots: []string{"/DATA"}, FileIndexScanIntervalH: 6,
	}
}

func TestSettingsStore_LoadNoFileUsesDefaults(t *testing.T) {
	s, err := LoadSettingsStore(filepath.Join(t.TempDir(), "none.json"), defaultsForTest())
	require.NoError(t, err)
	require.Equal(t, 5, s.Get().SemanticTopK)
	require.Equal(t, []string{"semantic", "filenames", "images"}, s.Get().DefaultSources)
}

func TestSettingsStore_PutPersistsAndReloads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	s, _ := LoadSettingsStore(p, defaultsForTest())
	in := s.Get()
	in.SemanticTopK = 9
	in.DefaultSources = []string{"images"}
	require.NoError(t, s.Put(in))
	require.Equal(t, 9, s.Get().SemanticTopK)
	// reload from disk → persisted
	s2, err := LoadSettingsStore(p, defaultsForTest())
	require.NoError(t, err)
	require.Equal(t, 9, s2.Get().SemanticTopK)
	require.Equal(t, []string{"images"}, s2.Get().DefaultSources)
}

func TestSettingsStore_PutRejectsEmptySources(t *testing.T) {
	s, _ := LoadSettingsStore(filepath.Join(t.TempDir(), "s.json"), defaultsForTest())
	in := s.Get()
	in.DefaultSources = []string{}
	require.Error(t, s.Put(in), "empty default_sources must be rejected, not silently treated as all")
}

func TestSettingsStore_PutRejectsBadValues(t *testing.T) {
	s, _ := LoadSettingsStore(filepath.Join(t.TempDir(), "s.json"), defaultsForTest())
	bad := s.Get()
	bad.SemanticTopK = 99
	require.Error(t, s.Put(bad))
	bad = s.Get()
	bad.DefaultSources = []string{"bogus"}
	require.Error(t, s.Put(bad))
	bad = s.Get()
	bad.FileIndexRoots = []string{"relative/path"}
	require.Error(t, s.Put(bad))
}

func TestSettingsStore_ApplyPatchOnlyProvided(t *testing.T) {
	s, _ := LoadSettingsStore(filepath.Join(t.TempDir(), "s.json"), defaultsForTest())
	nine := 9
	merged := s.Get().ApplyPatch(SearchSettingsPatch{SemanticTopK: &nine})
	require.Equal(t, 9, merged.SemanticTopK)
	require.Equal(t, 5, merged.FilenameTopK, "unprovided fields keep current value")
}

func TestLoadMigratesPreNotesFile(t *testing.T) {
	// simulate an old install: no schema_version, 3-source list, customized roots
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
        "default_sources": ["semantic","filenames","images"],
        "semantic_top_k": 7,
        "fileindex_roots": ["/DATA","/media/RAID"]
    }`), 0o644))
	st, err := LoadSettingsStore(path, defaultsWithNotes())
	require.NoError(t, err)
	got := st.Get()
	require.Equal(t, []string{"semantic", "filenames", "images", "notes"}, got.DefaultSources)
	require.Equal(t, 7, got.SemanticTopK)                                  // user value kept
	require.Equal(t, []string{"/DATA", "/media/RAID"}, got.FileIndexRoots) // user value kept
	require.Equal(t, SettingsSchemaVersion, got.SchemaVersion)
}

func TestLoadRespectsExplicitNotesRemoval(t *testing.T) {
	// a v2 file without "notes" = the user removed it via PUT; never re-add
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
        "schema_version": 2,
        "default_sources": ["semantic","filenames","images"]
    }`), 0o644))
	st, err := LoadSettingsStore(path, defaultsWithNotes())
	require.NoError(t, err)
	require.NotContains(t, st.Get().DefaultSources, "notes")
}

func TestPutStampsSchemaVersion(t *testing.T) {
	// regardless of what the client sends, the persisted blob carries the current version
	p := filepath.Join(t.TempDir(), "s.json")
	s, _ := LoadSettingsStore(p, defaultsForTest())
	in := s.Get()
	in.SchemaVersion = 0 // simulate a client/caller not setting it
	require.NoError(t, s.Put(in))
	require.Equal(t, SettingsSchemaVersion, s.Get().SchemaVersion)

	s2, err := LoadSettingsStore(p, defaultsForTest())
	require.NoError(t, err)
	require.Equal(t, SettingsSchemaVersion, s2.Get().SchemaVersion)
}

func TestSettingsStore_GetNotBlockedByPutIO(t *testing.T) {
	// Sanity: Put holds the rw lock only briefly; Get during a Put must not deadlock.
	s, _ := LoadSettingsStore(filepath.Join(t.TempDir(), "s.json"), defaultsForTest())
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			_ = s.Get()
		}()
	}
	in := s.Get()
	in.ImageTopK = 7
	close(ready) // release all Get goroutines, then race them against Put
	require.NoError(t, s.Put(in))
	wg.Wait()
	require.Equal(t, 7, s.Get().ImageTopK)
}
