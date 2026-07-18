package fileindex

import (
	"context"
	"log"
	"path/filepath"
	"sync"
	"time"
)

// Subsystem owns the filename-index runtime: the long-lived SQLite Index plus a
// restartable "scan + watch + reconcile" goroutine whose root set can be swapped
// live via ReloadRoots. Index and Watcher do not reference each other (SRP); the
// Subsystem coordinates them.
type Subsystem struct {
	mu      sync.Mutex // short critical sections: guards Watcher/stop/Roots (reload + Report)
	workMu  sync.Mutex // held by the indexing goroutine for its whole life: serializes scan work
	Index   *Index
	Watcher *Watcher      // current watcher (replaced on reload); nil when disabled
	Roots   []string      // current active roots (replaced on reload)
	stop    chan struct{} // current goroutine's stop signal (closed on reload/Close)

	normal    time.Duration // normal reconcile interval (from startup scan_interval_h)
	degraded  time.Duration // degraded reconcile interval (from cfg)
	onDegrade func()        // fired once when the watcher hits the inotify limit
}

type StatusReport struct {
	Status        string `json:"status"` // ready | scanning | disabled
	IndexedCount  int    `json:"indexed_count"`
	WatchDegraded bool   `json:"watch_degraded"`
}

// NewSubsystem builds an enabled subsystem. The caller starts indexing with
// StartInitial. normal/degraded are the reconcile intervals; onDegrade is fired
// once when the watcher degrades to scan-only (nil is allowed).
func NewSubsystem(idx *Index, normal, degraded time.Duration, onDegrade func()) *Subsystem {
	if onDegrade == nil {
		onDegrade = func() {}
	}
	return &Subsystem{Index: idx, normal: normal, degraded: degraded, onDegrade: onDegrade}
}

// StartInitial launches the indexing goroutine for the first time. It also runs
// PurgeOutside, clearing any rows left in the DB from a previous run that fall
// outside the current roots. Stopping any prior goroutine first keeps it safe
// against an accidental second call (which would otherwise leak a goroutine
// holding workMu and wedge all future indexing).
func (s *Subsystem) StartInitial(roots []string) {
	cleaned := cleanRoots(roots)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		close(s.stop)
	}
	s.startIndexing(cleaned)
}

// ReloadRoots swaps the active root set live: stops the old goroutine and starts
// a new one over the new roots (which purges-outside then scans). No-op (records
// Roots only) when the index is disabled.
func (s *Subsystem) ReloadRoots(newRoots []string) {
	cleaned := cleanRoots(newRoots)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Index == nil {
		s.Roots = cleaned
		return
	}
	if s.stop != nil {
		close(s.stop)
	}
	s.startIndexing(cleaned)
}

// startIndexing records a fresh watcher/stop/roots and launches the background
// goroutine. Caller MUST hold s.mu. roots are already cleaned.
func (s *Subsystem) startIndexing(roots []string) {
	stop := make(chan struct{})
	w := NewWatcher(s.Index, roots)
	w.SetOnDegrade(s.onDegrade)
	s.Watcher = w
	s.stop = stop
	s.Roots = roots
	idx := s.Index
	normal, degraded := s.normal, s.degraded
	go func() {
		// Bridge stop into a context so ScanInto/Reconcile's WalkDir throttling
		// is cancellable: closing stop (reload/Close) cancels ctx immediately
		// instead of waiting out an in-flight throttled sleep.
		ctx, cancel := context.WithCancel(context.Background())
		go func() { <-stop; cancel() }()
		defer cancel()

		// Serialize scan work: wait for the previous indexing goroutine (whose
		// stop is already closed) to release workMu, so concurrent ReloadRoots
		// never run two full PurgeOutside/BootScan passes at once.
		s.workMu.Lock()
		defer s.workMu.Unlock()
		// If a newer reload superseded us while we waited, bail BEFORE touching
		// the DB or starting a watcher — only the latest goroutine reconciles.
		select {
		case <-stop:
			return
		default:
		}
		if err := idx.PurgeOutside(roots); err != nil {
			log.Printf("fileindex: purge outside roots failed: %v", err)
		}
		select {
		case <-stop:
			return
		default:
		}
		if err := idx.BootScan(ctx, roots); err != nil {
			log.Printf("fileindex: boot scan failed: %v", err)
		}
		idx.SetReady() // mark ready once the first scan completes (idempotent on reload)
		select {
		case <-stop:
			return
		default:
		}
		_ = w.Start(stop)
		for {
			interval := normal
			if w.Degraded() {
				interval = degraded
			}
			select {
			case <-stop:
				return
			case <-time.After(interval):
				for _, r := range roots {
					_ = idx.Reconcile(ctx, r)
				}
			}
		}
	}()
}

// Close stops the current indexing goroutine and closes the index. Safe to call
// more than once. An in-flight scan (uninterruptible WalkDir) may still touch
// the DB after it is closed; those ops fail harmlessly (sql.ErrConnDone, logged
// or ignored) — acceptable on shutdown.
func (s *Subsystem) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	if s.Index == nil {
		return nil
	}
	idx := s.Index
	s.Index = nil
	return idx.Close()
}

func (s *Subsystem) Report() StatusReport {
	if s == nil {
		return StatusReport{Status: "disabled"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Index == nil {
		return StatusReport{Status: "disabled"}
	}
	count, _ := s.Index.Count(context.Background())
	degraded := false
	if s.Watcher != nil {
		degraded = s.Watcher.Degraded()
	}
	return StatusReport{Status: s.Index.Status(), IndexedCount: count, WatchDegraded: degraded}
}

// RescanActiveRoots reconciles each current active root in the background.
func (s *Subsystem) RescanActiveRoots() {
	if s == nil {
		return
	}
	s.mu.Lock()
	idx := s.Index
	roots := append([]string(nil), s.Roots...)
	s.mu.Unlock()
	if idx == nil {
		return
	}
	go func() {
		// Not wired to the indexing goroutine's stop channel: a manual rescan
		// isn't superseded by a reload the way the periodic loop is.
		for _, r := range roots {
			_ = idx.Reconcile(context.Background(), r)
		}
	}()
}

// cleanRoots normalizes each root via filepath.Clean (single choke point for
// path normalization, shared by StartInitial and ReloadRoots).
func cleanRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, filepath.Clean(r))
	}
	return out
}
