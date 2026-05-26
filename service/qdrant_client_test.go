package service

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func qdrantOrSkip(t *testing.T) string {
	host := os.Getenv("QDRANT_URL")
	if host == "" {
		t.Skip("set QDRANT_URL=http://127.0.0.1:6333 to run")
	}
	return host
}

func TestQdrantSearchTextHybrid(t *testing.T) {
	_ = qdrantOrSkip(t)
	c, err := NewQdrantClient("127.0.0.1", 6334)
	require.NoError(t, err)
	defer c.Close()

	res, err := c.SearchTextHybrid(context.Background(), QdrantSearchRequest{
		Collection: "text_chunks",
		Dense:      make([]float32, 1024),
		Sparse:     &Sparse{Indices: []int{1}, Values: []float32{0.5}},
		Filter:     &QdrantFilter{RootIDsAny: []string{"r1"}},
		Limit:      5,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestBuildPBFilter_MtimeAfter(t *testing.T) {
	f := &QdrantFilter{
		RootIDsAny:   []string{"r1"},
		MtimeAfterMs: 1750000000000,
	}
	pbf := buildPBFilter(f)
	require.NotNil(t, pbf)
	foundRange := false
	for _, c := range pbf.Must {
		fc := c.GetField()
		if fc == nil {
			continue
		}
		if r := fc.GetRange(); r != nil && fc.Key == "mtime_ms" {
			require.NotNil(t, r.Gte)
			require.Equal(t, float64(1750000000000), *r.Gte)
			foundRange = true
		}
	}
	require.True(t, foundRange, "expected Range condition on mtime_ms")
}
