package fileindex

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

// Watcher feeds filesystem changes into the Index. fsnotify delivery is thin;
// the testable logic lives in handleEvent/addWatchRecursive.
type Watcher struct {
	idx       *Index
	roots     []string
	fsw       *fsnotify.Watcher
	add       func(string) error // injectable for tests; defaults to fsw.Add
	degraded  atomic.Bool
	onDegrade func() // called once when watch limit is hit
}

func NewWatcher(idx *Index, roots []string) *Watcher {
	w := &Watcher{idx: idx, roots: roots, onDegrade: func() {}}
	w.add = func(p string) error { return errors.New("watcher not started") }
	return w
}

// SetOnDegrade sets the callback fired once when the watcher hits the kernel
// watch limit and degrades to scan-only. Call before Start.
func (w *Watcher) SetOnDegrade(f func()) { w.onDegrade = f }

func (w *Watcher) Degraded() bool { return w.degraded.Load() }

func isWatchLimit(err error) bool {
	var se *os.SyscallError
	if errors.As(err, &se) {
		return errors.Is(se.Err, syscall.ENOSPC) || errors.Is(se.Err, syscall.EMFILE) || errors.Is(se.Err, syscall.ENFILE)
	}
	return errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

// handleEvent updates the index for one fsnotify event.
func (w *Watcher) handleEvent(op fsnotify.Op, name string) {
	switch {
	case op&fsnotify.Create != 0:
		fi, err := os.Stat(name)
		if err != nil {
			return
		}
		if fi.IsDir() {
			if !skipDir(filepath.Base(name)) && !w.idx.excluded(name) {
				w.addWatchRecursive(name)
				// ScanInto skips its own root, so index the new dir itself too.
				_ = w.idx.Upsert(Record{
					Path: name, Name: fi.Name(), Root: w.rootOf(name),
					IsDir: true, MtimeMs: fi.ModTime().UnixMilli(),
				})
				// context.Background(): new-dir backfill trees are small; no need
				// to wire this into the subsystem's stop channel.
				_ = w.idx.ScanInto(context.Background(), name) // backfill cp -r / mv / tar x
			}
			return
		}
		w.upsertPath(name)
	case op&fsnotify.Write != 0:
		if fi, err := os.Stat(name); err == nil && !fi.IsDir() {
			w.upsertPath(name)
		}
	case op&(fsnotify.Remove|fsnotify.Rename) != 0:
		// fsnotify reports only the dir itself on rename/remove; cascade the subtree.
		_ = w.idx.DeletePrefix(name)
	}
}

func (w *Watcher) upsertPath(path string) {
	fi, err := os.Lstat(path)
	if err != nil || fi.IsDir() {
		return
	}
	if skipFile(filepath.Base(path)) {
		return
	}
	root := w.rootOf(path)
	_ = w.idx.Upsert(Record{
		Path: path, Name: fi.Name(), Root: root,
		Ext:  trimExt(fi.Name()),
		Size: fi.Size(), MtimeMs: fi.ModTime().UnixMilli(),
	})
}

func trimExt(name string) string {
	e := filepath.Ext(name)
	if e == "" {
		return ""
	}
	return name[len(name)-len(e)+1:]
}

func (w *Watcher) rootOf(path string) string {
	for _, r := range w.roots {
		if path == r || strings.HasPrefix(path, r+"/") {
			return r
		}
	}
	return ""
}

// addWatchRecursive adds inotify watches for dir and its subdirs. On hitting
// the kernel watch limit it flips to degraded mode (scan-only) and stops.
func (w *Watcher) addWatchRecursive(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if w.degraded.Load() {
			return filepath.SkipAll
		}
		if p != dir && (skipDir(filepath.Base(p)) || w.idx.excluded(p)) {
			return filepath.SkipDir
		}
		if aerr := w.add(p); aerr != nil {
			if isWatchLimit(aerr) {
				if w.degraded.CompareAndSwap(false, true) {
					w.onDegrade()
				}
				return filepath.SkipAll
			}
		}
		return nil
	})
}

// Start wires the real fsnotify watcher and runs the receive loop until stop is
// closed. roots already scanned by BootScan before Start is called.
func (w *Watcher) Start(stop <-chan struct{}) error {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.fsw = fsw
	w.add = fsw.Add
	for _, r := range w.roots {
		if _, err := os.Stat(r); err == nil {
			w.addWatchRecursive(r)
		}
	}
	go func() {
		defer fsw.Close()
		for {
			select {
			case <-stop:
				return
			case ev, ok := <-fsw.Events:
				if !ok {
					return
				}
				w.handleEvent(ev.Op, ev.Name)
			case _, ok := <-fsw.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return nil
}
