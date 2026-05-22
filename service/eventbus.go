package service

import (
	"encoding/json"
	"net"
	"sync/atomic"
	"time"
)

// EventBus is a best-effort fire-and-forget publisher to NimoOS-MessageBus
// via its Unix socket. Failures are silently dropped (Search service must
// not crash because MessageBus is down).
type EventBus struct {
	socketPath string
	// rolling counters for KPI events
	rerankCalls    atomic.Int64
	rerankFallback atomic.Int64
	queries        atomic.Int64
	cacheHits      atomic.Int64
}

func NewEventBus(socketPath string) *EventBus {
	return &EventBus{socketPath: socketPath}
}

type busMsg struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
	TS    int64          `json:"ts"`
}

func (b *EventBus) publish(event string, data map[string]any) {
	buf, _ := json.Marshal(busMsg{Event: event, Data: data, TS: time.Now().UnixMilli()})
	c, err := net.DialTimeout("unix", b.socketPath, 200*time.Millisecond)
	if err != nil {
		return
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	_, _ = c.Write(buf)
}

// PublishWarning emits Search:Warning{kind, detail}. Use for embedder/qdrant
// outages and degraded states.
func (b *EventBus) PublishWarning(kind, detail string) {
	b.publish("Search:Warning", map[string]any{"kind": kind, "detail": detail})
}

// RecordRerank tracks rerank attempts and fallbacks. PublishStats reports.
func (b *EventBus) RecordRerank(fallback bool) {
	b.rerankCalls.Add(1)
	if fallback {
		b.rerankFallback.Add(1)
	}
}

func (b *EventBus) RecordQuery(cacheHit bool) {
	b.queries.Add(1)
	if cacheHit {
		b.cacheHits.Add(1)
	}
}

// PublishStatsSnapshot called every 60s by a goroutine started in main.go.
// MVP: just publishes rerank-fallback rate + cache-hit rate.
func (b *EventBus) PublishStatsSnapshot() {
	rc := b.rerankCalls.Load()
	if rc > 0 {
		b.publish("Search:RerankFallbackRate", map[string]any{
			"rate": float64(b.rerankFallback.Load()) / float64(rc),
		})
	}
	q := b.queries.Load()
	if q > 0 {
		b.publish("Search:CacheHitRate", map[string]any{
			"hit_rate":  float64(b.cacheHits.Load()) / float64(q),
			"evictions": 0,
		})
	}
}
