// Package fileindex maintains a SQLite-backed filename index over the
// authorized roots, refreshed by a boot scan, an fsnotify watcher, and a
// periodic reconcile. Substring matching uses FTS5 trigram when the bundled
// SQLite has it, else falls back to a name_lower LIKE scan.
package fileindex

import (
	"context"
	"database/sql"
	"strings"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

type Record struct {
	Path    string
	Name    string
	Ext     string
	Root    string
	Size    int64
	MtimeMs int64
	IsDir   bool
}

type FileNameHit struct {
	Path    string  `json:"path"`
	Name    string  `json:"name"`
	Ext     string  `json:"ext"`
	Size    int64   `json:"size"`
	MtimeMs int64   `json:"mtime_ms"`
	IsDir   bool    `json:"is_dir"`
	Match   float64 `json:"match"`
}

type Index struct {
	db      *sql.DB
	ftsOK   bool
	ready   atomic.Bool
}

// Open opens (creating if needed) the SQLite database at path with WAL +
// busy_timeout, creates the schema, and probes FTS5 trigram availability.
func Open(path string) (*Index, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc single-writer; serialize to avoid SQLITE_BUSY
	for _, p := range []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA busy_timeout=5000;`,
	} {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS file_index (
  path       TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  name_lower TEXT NOT NULL,
  ext        TEXT,
  size       INTEGER,
  mtime_ms   INTEGER,
  root       TEXT NOT NULL,
  is_dir     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_file_index_name_lower ON file_index(name_lower);
CREATE INDEX IF NOT EXISTS idx_file_index_root ON file_index(root);
`); err != nil {
		_ = db.Close()
		return nil, err
	}
	idx := &Index{db: db}
	// Probe FTS5 trigram. If unavailable, ftsOK stays false and Search uses
	// the name_lower LIKE fallback (see spec §4.1).
	if _, err := db.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS file_name_fts USING fts5(name, path UNINDEXED, tokenize='trigram');`); err == nil {
		idx.ftsOK = true
	}
	return idx, nil
}

func (i *Index) Close() error { return i.db.Close() }

func (i *Index) Status() string {
	if i.ready.Load() {
		return "ready"
	}
	return "scanning"
}
func (i *Index) SetReady() { i.ready.Store(true) }

func (i *Index) Upsert(r Record) error {
	dir := 0
	if r.IsDir {
		dir = 1
	}
	if _, err := i.db.Exec(`INSERT INTO file_index(path,name,name_lower,ext,size,mtime_ms,root,is_dir)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(path) DO UPDATE SET name=excluded.name,name_lower=excluded.name_lower,
		  ext=excluded.ext,size=excluded.size,mtime_ms=excluded.mtime_ms,root=excluded.root,is_dir=excluded.is_dir`,
		r.Path, r.Name, strings.ToLower(r.Name), r.Ext, r.Size, r.MtimeMs, r.Root, dir); err != nil {
		return err
	}
	if i.ftsOK {
		_, _ = i.db.Exec(`DELETE FROM file_name_fts WHERE path=?`, r.Path)
		if _, err := i.db.Exec(`INSERT INTO file_name_fts(name,path) VALUES(?,?)`, r.Name, r.Path); err != nil {
			return err
		}
	}
	return nil
}

// DeletePrefix removes the exact path and any descendant under path/. Matching
// on path||'/' (not path||'%') prevents /DATA/old matching /DATA/old2.
func (i *Index) DeletePrefix(path string) error {
	like := path + "/%"
	if _, err := i.db.Exec(`DELETE FROM file_index WHERE path=? OR path LIKE ?`, path, like); err != nil {
		return err
	}
	if i.ftsOK {
		_, _ = i.db.Exec(`DELETE FROM file_name_fts WHERE path=? OR path LIKE ?`, path, like)
	}
	return nil
}

func (i *Index) Count(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_index`).Scan(&n)
	return n, err
}
