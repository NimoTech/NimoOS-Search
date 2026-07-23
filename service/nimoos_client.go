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

// NimoOSClient 向核心(NimoOS 主服务)拉取用户被授权的 root_ids。
// 用于替代原来经由 Wiki 转发的 WikiClient.UserRoots(见 Task 8)。
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

// SearchRoots 返回给定用户被授权访问的 root_ids,按 userID 缓存 cacheTTL。
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
