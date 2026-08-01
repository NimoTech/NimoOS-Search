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

// NimoOSClient pulls the root_ids a user is authorized for from core (the
// main NimoOS service). Replaces the old WikiClient.UserRoots that went
// through Wiki (see Task 8).
type NimoOSClient struct {
	src      *BaseURLSource
	hc       *http.Client
	cacheTTL time.Duration

	mu    sync.RWMutex
	cache map[string]nimoosRootsCacheEntry
}

type nimoosRootsCacheEntry struct {
	roots []string
	exp   time.Time
}

func NewNimoOSClient(src *BaseURLSource, cacheTTL time.Duration) *NimoOSClient {
	return &NimoOSClient{
		src:      src,
		hc:       &http.Client{Timeout: 5 * time.Second},
		cacheTTL: cacheTTL,
		cache:    map[string]nimoosRootsCacheEntry{},
	}
}

// SearchRoots returns the root_ids the given user is authorized to access,
// cached per userID for cacheTTL.
func (c *NimoOSClient) SearchRoots(ctx context.Context, userID string) ([]string, error) {
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
			base+"/v1/nimoos/search-roots?"+q.Encode(), nil)
	})
	if err != nil {
		return nil, fmt.Errorf("nimoos search-roots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("nimoos search-roots %d", resp.StatusCode)
	}
	var out struct {
		RootIDs []string `json:"root_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[userID] = nimoosRootsCacheEntry{roots: out.RootIDs, exp: time.Now().Add(c.cacheTTL)}
	c.mu.Unlock()
	return out.RootIDs, nil
}
