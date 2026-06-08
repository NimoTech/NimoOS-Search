// Package fileindex maintains a SQLite-backed filename index over the
// authorized roots, refreshed by a boot scan, an fsnotify watcher, and a
// periodic reconcile. Substring matching uses FTS5 trigram when the bundled
// SQLite has it, else falls back to a name_lower LIKE scan.
package fileindex

import (
	"context"
	"database/sql"
	"sort"
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
	db    *sql.DB
	ftsOK bool
	ready atomic.Bool
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

// PurgeOutside removes every indexed row whose path is neither equal to nor a
// descendant of any given root. Used on a root-set change to drop entries that
// fall outside the new roots (e.g. removed system/mount paths) WITHOUT deleting
// or re-scanning the retained roots' rows. Empty roots clears everything.
//
// Uses substr+'=' (BINARY collation, case-sensitive) for the prefix test rather
// than LIKE: SQLite's LIKE is case-insensitive by default (this DB sets no
// case_sensitive_like pragma), which would treat /data as inside /DATA and fail
// to purge it; substr also avoids GLOB treating [ * ? in a root as wildcards.
func (i *Index) PurgeOutside(roots []string) error {
	if len(roots) == 0 {
		if _, err := i.db.Exec(`DELETE FROM file_index`); err != nil {
			return err
		}
		if i.ftsOK {
			_, _ = i.db.Exec(`DELETE FROM file_name_fts`)
		}
		return nil
	}
	conds := make([]string, 0, len(roots))
	args := make([]any, 0, len(roots)*3)
	for _, r := range roots {
		conds = append(conds, "path = ? OR substr(path, 1, length(?)) = ?")
		args = append(args, r, r+"/", r+"/")
	}
	inside := strings.Join(conds, " OR ")
	if _, err := i.db.Exec(`DELETE FROM file_index WHERE NOT (`+inside+`)`, args...); err != nil {
		return err
	}
	if i.ftsOK {
		// schema 无 AFTER DELETE 触发器,FTS 影子表须手动同删(与 DeletePrefix 一致)
		_, _ = i.db.Exec(`DELETE FROM file_name_fts WHERE NOT (`+inside+`)`, args...)
	}
	return nil
}

func (i *Index) Count(ctx context.Context) (int, error) {
	var n int
	err := i.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM file_index`).Scan(&n)
	return n, err
}

// Search returns up to topK filename hits ranked by match score then mtime.
// Candidate retrieval uses FTS5 trigram when available, else a name_lower scan.
func (i *Index) Search(ctx context.Context, query string, topK int) ([]FileNameHit, error) {
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil, nil
	}
	rows, err := i.candidateRows(ctx, terms)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var hits []FileNameHit
	for rows.Next() {
		var h FileNameHit
		var nameLower string
		var dir int
		if err := rows.Scan(&h.Path, &h.Name, &nameLower, &h.Ext, &h.Size, &h.MtimeMs, &dir); err != nil {
			return nil, err
		}
		h.IsDir = dir == 1
		h.Match = scoreName(nameLower, terms)
		if h.Match > 0 {
			hits = append(hits, h)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].Match != hits[b].Match {
			return hits[a].Match > hits[b].Match
		}
		return hits[a].MtimeMs > hits[b].MtimeMs
	})
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// candidateRows returns rows whose name matches ANY term (final scoring/AND
// weighting happens in Go). Selects the same columns regardless of backend.
// Note: the FTS5 trigram path requires each term to be >= 3 chars; the LIKE
// fallback has no minimum length, so very short queries may return fewer results
// on the FTS path than on the fallback path.
func (i *Index) candidateRows(ctx context.Context, terms []string) (*sql.Rows, error) {
	const cols = `path,name,name_lower,ext,size,mtime_ms,is_dir`
	if i.ftsOK {
		// trigram MATCH on any term; OR-join with quoted phrases.
		var qs []string
		var args []any
		for _, t := range terms {
			qs = append(qs, `"`+strings.ReplaceAll(t, `"`, ``)+`"`)
		}
		args = append(args, strings.Join(qs, " OR "))
		return i.db.QueryContext(ctx,
			`SELECT f.path,f.name,f.name_lower,f.ext,f.size,f.mtime_ms,f.is_dir FROM file_name_fts m JOIN file_index f ON f.path=m.path WHERE file_name_fts MATCH ?`, args...)
	}
	// Fallback: name_lower LIKE for any term.
	var where []string
	var args []any
	for _, t := range terms {
		where = append(where, `name_lower LIKE '%' || ? || '%'`)
		args = append(args, t)
	}
	return i.db.QueryContext(ctx,
		`SELECT `+cols+` FROM file_index WHERE `+strings.Join(where, " OR "), args...)
}
