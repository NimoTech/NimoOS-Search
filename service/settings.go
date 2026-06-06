package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SearchSettings is the runtime-mutable subset of search behavior. The first
// five fields are hot (read live by the Aggregator); the FileIndex* fields are
// persisted but applied on restart (see spec §6).
type SearchSettings struct {
	DefaultSources         []string `json:"default_sources"`
	SemanticTopK           int      `json:"semantic_top_k"`
	FilenameTopK           int      `json:"filename_top_k"`
	ImageTopK              int      `json:"image_top_k"`
	MaxTotalResults        int      `json:"max_total_results"`
	FileIndexEnabled       bool     `json:"fileindex_enabled"`
	FileIndexRoots         []string `json:"fileindex_roots"`
	FileIndexScanIntervalH int      `json:"fileindex_scan_interval_h"`
}

// SearchSettingsPatch is the PUT body: pointer fields distinguish "not provided"
// (nil → keep current) from an explicit value.
type SearchSettingsPatch struct {
	DefaultSources         *[]string `json:"default_sources"`
	SemanticTopK           *int      `json:"semantic_top_k"`
	FilenameTopK           *int      `json:"filename_top_k"`
	ImageTopK              *int      `json:"image_top_k"`
	MaxTotalResults        *int      `json:"max_total_results"`
	FileIndexEnabled       *bool     `json:"fileindex_enabled"`
	FileIndexRoots         *[]string `json:"fileindex_roots"`
	FileIndexScanIntervalH *int      `json:"fileindex_scan_interval_h"`
}

func (s SearchSettings) ApplyPatch(p SearchSettingsPatch) SearchSettings {
	if p.DefaultSources != nil {
		s.DefaultSources = *p.DefaultSources
	}
	if p.SemanticTopK != nil {
		s.SemanticTopK = *p.SemanticTopK
	}
	if p.FilenameTopK != nil {
		s.FilenameTopK = *p.FilenameTopK
	}
	if p.ImageTopK != nil {
		s.ImageTopK = *p.ImageTopK
	}
	if p.MaxTotalResults != nil {
		s.MaxTotalResults = *p.MaxTotalResults
	}
	if p.FileIndexEnabled != nil {
		s.FileIndexEnabled = *p.FileIndexEnabled
	}
	if p.FileIndexRoots != nil {
		s.FileIndexRoots = *p.FileIndexRoots
	}
	if p.FileIndexScanIntervalH != nil {
		s.FileIndexScanIntervalH = *p.FileIndexScanIntervalH
	}
	return s
}

var validSources = map[string]bool{"semantic": true, "filenames": true, "images": true}

func validate(in SearchSettings) error {
	if len(in.DefaultSources) == 0 {
		return fmt.Errorf("default_sources must contain at least one source")
	}
	for _, s := range in.DefaultSources {
		if !validSources[s] {
			return fmt.Errorf("invalid source %q", s)
		}
	}
	for name, v := range map[string]int{"semantic_top_k": in.SemanticTopK, "filename_top_k": in.FilenameTopK, "image_top_k": in.ImageTopK} {
		if v < 1 || v > 20 {
			return fmt.Errorf("%s must be in [1,20]", name)
		}
	}
	if in.MaxTotalResults < 1 || in.MaxTotalResults > 60 {
		return fmt.Errorf("max_total_results must be in [1,60]")
	}
	if in.FileIndexScanIntervalH < 0 {
		return fmt.Errorf("fileindex_scan_interval_h must be >= 0")
	}
	for _, r := range in.FileIndexRoots {
		if !strings.HasPrefix(r, "/") {
			return fmt.Errorf("fileindex root %q must be an absolute path", r)
		}
	}
	return nil
}

type SettingsStore struct {
	rw   sync.RWMutex
	wmu  sync.Mutex
	cur  SearchSettings
	path string
}

// LoadSettingsStore starts from defaults, then overlays any persisted override
// file (field-by-field via json.Unmarshal into a copy of defaults).
func LoadSettingsStore(path string, defaults SearchSettings) (*SettingsStore, error) {
	cur := defaults
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &cur); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if len(cur.DefaultSources) == 0 { // file omitted/empty → keep default semantics
			cur.DefaultSources = defaults.DefaultSources
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &SettingsStore{cur: cur, path: path}, nil
}

func (s *SettingsStore) Get() SearchSettings {
	s.rw.RLock()
	defer s.rw.RUnlock()
	return s.cur // SearchSettings is a value; slices are shared but treated read-only
}

// Put validates, then writes to disk OUTSIDE the rw lock (so concurrent Get() —
// every search — is never blocked on I/O), then swaps the in-memory snapshot
// under a brief write lock. wmu serializes concurrent Puts' file writes.
func (s *SettingsStore) Put(in SearchSettings) error {
	if err := validate(in); err != nil {
		return err
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err := s.writeFile(in); err != nil {
		return err
	}
	s.rw.Lock()
	s.cur = in
	s.rw.Unlock()
	return nil
}

func (s *SettingsStore) writeFile(in SearchSettings) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return err
	}
	return nil
}
