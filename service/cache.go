package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
	"golang.org/x/sync/singleflight"
)

type EmbedCache struct {
	lru *lru.Cache[string, embedCacheEntry]
	ttl time.Duration
	sf  singleflight.Group
	mu  sync.RWMutex
}

type embedCacheEntry struct {
	val *EmbedResult
	exp time.Time
}

func NewEmbedCache(size int, ttl time.Duration) *EmbedCache {
	c, _ := lru.New[string, embedCacheEntry](size)
	return &EmbedCache{lru: c, ttl: ttl}
}

func (c *EmbedCache) Get(key string) (*EmbedResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.lru.Get(key)
	if !ok || time.Now().After(e.exp) {
		return nil, false
	}
	return e.val, true
}

func (c *EmbedCache) Put(key string, v *EmbedResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, embedCacheEntry{val: v, exp: time.Now().Add(c.ttl)})
}

// GetOrLoad returns the cached embedding for key, or invokes loader and stores
// the result. Concurrent calls with the same key are coalesced via singleflight
// so a cold cache + N concurrent queries hits the loader only once.
func (c *EmbedCache) GetOrLoad(ctx context.Context, key string,
	loader func(context.Context) (*EmbedResult, error)) (*EmbedResult, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	v, err, _ := c.sf.Do(key, func() (any, error) {
		// Double-check in case another goroutine populated cache mid-flight.
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		out, err := loader(ctx)
		if err != nil {
			return nil, err
		}
		c.Put(key, out)
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*EmbedResult), nil
}

// HashQuery returns the canonical cache key for a query string.
// Exported so callers can hash once and reuse.
func HashQuery(q string) string {
	return sha256Hex(q)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
