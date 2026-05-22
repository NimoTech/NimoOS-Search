package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func qhash(q string) string {
	s := sha256.Sum256([]byte(q))
	return hex.EncodeToString(s[:])
}

func TestEmbedCacheHitMiss(t *testing.T) {
	cache := NewEmbedCache(10, 1*time.Hour)

	_, ok := cache.Get(qhash("hello"))
	require.False(t, ok)

	cache.Put(qhash("hello"), &EmbedResult{Dense: []float32{0.1}, ModelVersion: "v1"})
	got, ok := cache.Get(qhash("hello"))
	require.True(t, ok)
	require.Equal(t, "v1", got.ModelVersion)
}

func TestEmbedCacheTTLExpiry(t *testing.T) {
	cache := NewEmbedCache(10, 10*time.Millisecond)
	cache.Put("k", &EmbedResult{Dense: []float32{0.1}, ModelVersion: "v1"})
	time.Sleep(20 * time.Millisecond)
	_, ok := cache.Get("k")
	require.False(t, ok)
}

func TestEmbedCacheSingleflightDedupesConcurrentMisses(t *testing.T) {
	cache := NewEmbedCache(10, 1*time.Hour)
	var calls int64
	loader := func(ctx context.Context) (*EmbedResult, error) {
		atomic.AddInt64(&calls, 1)
		time.Sleep(10 * time.Millisecond)
		return &EmbedResult{Dense: []float32{0.1}, ModelVersion: "v1"}, nil
	}

	const N = 10
	results := make(chan *EmbedResult, N)
	for i := 0; i < N; i++ {
		go func() {
			r, err := cache.GetOrLoad(context.Background(), "key1", loader)
			require.NoError(t, err)
			results <- r
		}()
	}
	for i := 0; i < N; i++ {
		<-results
	}
	require.Equal(t, int64(1), atomic.LoadInt64(&calls),
		"singleflight should collapse concurrent loads")
}
