package fileindex

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// skipDir returns true for hidden or known container dirs we never index.
func skipDir(base string) bool {
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch base {
	case "node_modules", "@eaDir", ".git", "__pycache__":
		return true
	}
	return false
}

func skipFile(base string) bool { return strings.HasPrefix(base, ".") }

func recordFor(path, root string, d fs.DirEntry) Record {
	r := Record{Path: path, Name: d.Name(), Root: root, IsDir: d.IsDir()}
	if !d.IsDir() {
		r.Ext = strings.TrimPrefix(strings.ToLower(filepath.Ext(d.Name())), ".")
		if fi, err := d.Info(); err == nil {
			r.Size = fi.Size()
			r.MtimeMs = fi.ModTime().UnixMilli()
		}
	}
	return r
}

// BootScan walks each root and upserts every (non-hidden) entry.
func (i *Index) BootScan(roots []string) error {
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue // root not mounted; skip
		}
		if err := i.ScanInto(root); err != nil {
			return err
		}
	}
	return nil
}

// ScanInto walks dir and upserts entries (used for boot scan and for new-dir
// backfill from the watcher).
func (i *Index) ScanInto(dir string) error {
	rootOf := dir
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := filepath.Base(p)
		if d.IsDir() {
			if p != dir && skipDir(base) {
				return filepath.SkipDir
			}
			if p == dir {
				return nil // don't index the scan root itself
			}
		} else if skipFile(base) {
			return nil
		}
		return i.Upsert(recordFor(p, rootOf, d))
	})
}

// Reconcile re-walks root, upserts what's on disk, and deletes index rows under
// root that no longer exist (drift correction).
func (i *Index) Reconcile(root string) error {
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	onDisk := map[string]struct{}{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := filepath.Base(p)
		if d.IsDir() {
			if p != root && skipDir(base) {
				return filepath.SkipDir
			}
			if p == root {
				return nil
			}
		} else if skipFile(base) {
			return nil
		}
		onDisk[p] = struct{}{}
		return i.Upsert(recordFor(p, root, d))
	})
	if err != nil {
		return err
	}
	// delete index rows under root that are gone
	rows, err := i.db.QueryContext(context.Background(),
		`SELECT path FROM file_index WHERE path=? OR path LIKE ?`, root, root+"/%")
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if _, ok := onDisk[p]; !ok {
			gone = append(gone, p)
		}
	}
	rows.Close()
	for _, p := range gone {
		if err := i.DeletePrefix(p); err != nil {
			return err
		}
	}
	return nil
}
