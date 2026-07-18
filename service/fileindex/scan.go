package fileindex

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// walkThrottle paces a WalkDir loop: after every `every` visited entries it
// sleeps `sleep` (cancellable via ctx). every<=0 disables throttling (tick
// still observes ctx cancellation but never sleeps) — this is what keeps a
// zero-value Index untouched by throttling.
type walkThrottle struct {
	every int
	sleep time.Duration
	n     int
}

func (t *walkThrottle) tick(ctx context.Context) error {
	if t.every <= 0 {
		return ctx.Err()
	}
	t.n++
	if t.n%t.every != 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(t.sleep):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

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
func (i *Index) BootScan(ctx context.Context, roots []string) error {
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue // root not mounted; skip
		}
		if err := i.ScanInto(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

// ScanInto walks dir and upserts entries (used for boot scan and for new-dir
// backfill from the watcher). ctx paces the throttle and cancels the walk.
func (i *Index) ScanInto(ctx context.Context, dir string) error {
	th := &walkThrottle{every: i.ThrottleEvery, sleep: i.ThrottleSleep}
	rootOf := dir
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if terr := th.tick(ctx); terr != nil {
			return terr
		}
		if err != nil {
			return nil
		}
		base := filepath.Base(p)
		if d.IsDir() {
			if p != dir && (skipDir(base) || i.excluded(p)) {
				return filepath.SkipDir
			}
			if p == dir {
				return nil // don't index the scan root itself
			}
		} else if skipFile(base) || i.excluded(p) {
			return nil
		}
		return i.Upsert(recordFor(p, rootOf, d))
	})
}

// Reconcile re-walks root, upserts what's on disk, and deletes index rows under
// root that no longer exist (drift correction). ctx paces the throttle and
// cancels both the walk and the gone-rows deletion loop.
func (i *Index) Reconcile(ctx context.Context, root string) error {
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	th := &walkThrottle{every: i.ThrottleEvery, sleep: i.ThrottleSleep}
	onDisk := map[string]struct{}{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if terr := th.tick(ctx); terr != nil {
			return terr
		}
		if err != nil {
			return nil
		}
		base := filepath.Base(p)
		if d.IsDir() {
			if p != root && (skipDir(base) || i.excluded(p)) {
				return filepath.SkipDir
			}
			if p == root {
				return nil
			}
		} else if skipFile(base) || i.excluded(p) {
			return nil
		}
		onDisk[p] = struct{}{}
		return i.Upsert(recordFor(p, root, d))
	})
	if err != nil {
		return err
	}
	// delete index rows under root that are gone
	rows, err := i.db.QueryContext(ctx,
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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := i.DeletePrefix(p); err != nil {
			return err
		}
	}
	return nil
}
