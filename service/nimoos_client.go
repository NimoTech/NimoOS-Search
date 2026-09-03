package service

import (
	"context"
	"encoding/json"
	"errors"
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
	roots      []string
	paths      []string // filesystem paths of the granted roots (virtual roots omitted)
	pathsKnown bool     // false when core predates the "roots" field
	exp        time.Time
}

// ErrRootPathsUnavailable is returned by SearchRootPaths when core answered
// with root_ids only (an older build without the "roots" field). Callers must
// fail closed rather than treat it as "no roots".
var ErrRootPathsUnavailable = errors.New("nimoos search-roots: root paths unavailable (core too old)")

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
	e, err := c.entry(ctx, userID)
	if err != nil {
		return nil, err
	}
	return e.roots, nil
}

// SearchRootPaths returns the filesystem paths of the user's granted roots
// (same fetch/cache as SearchRoots). Used to scope the filename index.
func (c *NimoOSClient) SearchRootPaths(ctx context.Context, userID string) ([]string, error) {
	e, err := c.entry(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !e.pathsKnown {
		return nil, ErrRootPathsUnavailable
	}
	return e.paths, nil
}

func (c *NimoOSClient) entry(ctx context.Context, userID string) (nimoosRootsCacheEntry, error) {
	c.mu.RLock()
	if e, ok := c.cache[userID]; ok && time.Now().Before(e.exp) {
		c.mu.RUnlock()
		return e, nil
	}
	c.mu.RUnlock()

	q := url.Values{}
	q.Set("user_id", userID)
	resp, err := doWithRediscover(c.hc, c.src, func(base string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/v1/nimoos/search-roots?"+q.Encode(), nil)
	})
	if err != nil {
		return nimoosRootsCacheEntry{}, fmt.Errorf("nimoos search-roots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nimoosRootsCacheEntry{}, fmt.Errorf("nimoos search-roots %d", resp.StatusCode)
	}
	var out struct {
		RootIDs []string `json:"root_ids"`
		Roots   []struct {
			RootID string `json:"root_id"`
			Path   string `json:"path"`
		} `json:"roots"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nimoosRootsCacheEntry{}, err
	}

	e := nimoosRootsCacheEntry{roots: out.RootIDs, pathsKnown: out.Roots != nil, exp: time.Now().Add(c.cacheTTL)}
	for _, r := range out.Roots {
		if r.Path != "" {
			e.paths = append(e.paths, r.Path)
		}
	}
	c.mu.Lock()
	c.cache[userID] = e
	c.mu.Unlock()
	return e, nil
}
