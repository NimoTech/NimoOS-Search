package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type WikiClient struct {
	src      *BaseURLSource
	hc       *http.Client
	cacheTTL time.Duration

	mu    sync.RWMutex
	cache map[string]userRootsCacheEntry
}

type userRootsCacheEntry struct {
	roots []string
	exp   time.Time
}

func NewWikiClient(src *BaseURLSource, timeoutSec int, cacheTTL time.Duration) *WikiClient {
	return &WikiClient{
		src:      src,
		hc:       &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		cacheTTL: cacheTTL,
		cache:    map[string]userRootsCacheEntry{},
	}
}

// UserRoots returns the root_ids the given user is allowed to access.
// Cached per user_id for cacheTTL.
func (c *WikiClient) UserRoots(ctx context.Context, userID string) ([]string, error) {
	c.mu.RLock()
	if e, ok := c.cache[userID]; ok && time.Now().Before(e.exp) {
		c.mu.RUnlock()
		return e.roots, nil
	}
	c.mu.RUnlock()

	q := url.Values{}
	q.Set("user_id", userID)
	resp, err := doWithRediscover(c.hc, c.src, func(base string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/v1/wiki/_internal/user-roots?"+q.Encode(), nil)
	})
	if err != nil {
		return nil, fmt.Errorf("wiki user-roots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("wiki user-roots %d", resp.StatusCode)
	}
	var out struct {
		RootIDs []string `json:"root_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[userID] = userRootsCacheEntry{roots: out.RootIDs, exp: time.Now().Add(c.cacheTTL)}
	c.mu.Unlock()
	return out.RootIDs, nil
}

// InvalidateCache drops cached user_id (used by /_internal/warm or tests).
func (c *WikiClient) InvalidateCache(userID string) {
	c.mu.Lock()
	delete(c.cache, userID)
	c.mu.Unlock()
}
