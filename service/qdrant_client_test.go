package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	c, err := NewQdrantClient("127.0.0.1", 6334, "http://127.0.0.1:6333")
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

// TestDistinctValues_UsesFacetREST pins the REST facet contract: POST
// /collections/{c}/facet with {key, limit, exact}, values read from
// result.hits[].value, non-string values ignored.
func TestDistinctValues_UsesFacetREST(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"result":{"hits":[{"value":"text/markdown","count":3},{"value":7,"count":1},{"value":"video/mp4","count":2}]},"status":"ok"}`))
	}))
	defer srv.Close()
	c, err := NewQdrantClient("127.0.0.1", 6334, srv.URL+"/")
	require.NoError(t, err)
	defer c.Close()
	vals, err := c.DistinctValues(context.Background(), "text_chunks", "mime")
	require.NoError(t, err)
	require.Equal(t, "/collections/text_chunks/facet", gotPath)
	require.Equal(t, "mime", gotBody["key"])
	require.Equal(t, true, gotBody["exact"])
	require.Equal(t, []string{"text/markdown", "video/mp4"}, vals)
}

func TestDistinctValues_NonOKIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, err := NewQdrantClient("127.0.0.1", 6334, srv.URL)
	require.NoError(t, err)
	defer c.Close()
	_, err = c.DistinctValues(context.Background(), "text_chunks", "mime")
	require.Error(t, err)
}
