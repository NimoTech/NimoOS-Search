package service

import (
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// EventBus publishes events to the NimoOS MessageBus over a Unix socket.
// All publish calls are fire-and-forget: failures are silently discarded.
type EventBus struct {
	sockPath string

	// KPI counters — updated by callers, snapshotted by PublishStatsSnapshot.
	QueryCount  atomic.Int64
	IndexedDocs atomic.Int64
}

// NewEventBus returns an EventBus that sends to the given Unix socket path.
func NewEventBus(sockPath string) *EventBus {
	return &EventBus{sockPath: sockPath}
}

// mbEvent is the wire format understood by NimoOS MessageBus.
type mbEvent struct {
	Name    string         `json:"name"`
	Payload map[string]any `json:"payload"`
}

// publish sends one event to the MessageBus socket, best-effort.
func (eb *EventBus) publish(name string, payload map[string]any) {
	conn, err := net.DialTimeout("unix", eb.sockPath, 2*time.Second)
	if err != nil {
		// Socket absent (e.g. MessageBus not running) — discard silently.
		return
	}
	defer conn.Close()
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	data, err := json.Marshal(mbEvent{Name: name, Payload: payload})
	if err != nil {
		return
	}
	_, _ = conn.Write(data)
}

// PublishWarning fires a "Search:Warning" event with the given key and detail.
func (eb *EventBus) PublishWarning(key, detail string) {
	eb.publish("Search:Warning", map[string]any{
		"key":    key,
		"detail": detail,
		"ts":     time.Now().UTC().Format(time.RFC3339),
	})
}

// PublishStatsSnapshot fires a "Search:StatsSnapshot" event with current KPI counters.
func (eb *EventBus) PublishStatsSnapshot() {
	eb.publish("Search:StatsSnapshot", map[string]any{
		"query_count":  eb.QueryCount.Load(),
		"indexed_docs": eb.IndexedDocs.Load(),
		"ts":           fmt.Sprintf("%d", time.Now().Unix()),
	})
}
