package fileindex

import "context"

// Subsystem aggregates the filename-index pieces for status/rescan handlers
// without coupling Index and Watcher to each other (they don't hold references
// to one another). Roots is the active root set captured at startup — rescan
// uses THIS, not any later-edited setting (see spec §2.2/§6).
type Subsystem struct {
	Index   *Index
	Watcher *Watcher
	Roots   []string
}

type StatusReport struct {
	Status        string `json:"status"` // ready | scanning | disabled
	IndexedCount  int    `json:"indexed_count"`
	WatchDegraded bool   `json:"watch_degraded"`
}

func (s *Subsystem) Report() StatusReport {
	if s == nil || s.Index == nil {
		return StatusReport{Status: "disabled"}
	}
	count, _ := s.Index.Count(context.Background())
	degraded := false
	if s.Watcher != nil {
		degraded = s.Watcher.Degraded()
	}
	return StatusReport{Status: s.Index.Status(), IndexedCount: count, WatchDegraded: degraded}
}

// RescanActiveRoots reconciles each active root (captured at startup) in the
// background. No-op if disabled.
func (s *Subsystem) RescanActiveRoots() {
	if s == nil || s.Index == nil {
		return
	}
	roots := s.Roots
	idx := s.Index
	go func() {
		for _, r := range roots {
			_ = idx.Reconcile(r)
		}
	}()
}
