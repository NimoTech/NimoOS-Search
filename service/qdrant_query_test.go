package service

import (
	"testing"

	pb "github.com/qdrant/go-client/qdrant"
	"github.com/stretchr/testify/require"
)

func hybridReq(sparse bool) QdrantSearchRequest {
	r := QdrantSearchRequest{
		Collection: "text_chunks",
		Dense:      []float32{0.1, 0.2},
		Filter:     &QdrantFilter{RootIDsAny: []string{"r1"}, KindIn: []string{"body"}},
		Limit:      8,
	}
	if sparse {
		r.Sparse = &Sparse{Indices: []int{3}, Values: []float32{0.7}}
	}
	return r
}

// Dense and sparse must be fused with RRF as peers. The previous shape used
// the sparse leg as a prefetch gate that dense merely re-scored, so a strong
// dense-only match outside the sparse top-N could never be returned.
func TestBuildHybridQuery_FusesDenseAndSparseWithRRF(t *testing.T) {
	q := buildHybridQuery(hybridReq(true))
	fusion, ok := q.GetQuery().GetVariant().(*pb.Query_Fusion)
	require.True(t, ok, "top-level query must be a fusion, got %T", q.GetQuery().GetVariant())
	require.Equal(t, pb.Fusion_RRF, fusion.Fusion)
	require.Len(t, q.GetPrefetch(), 2)
	using := map[string]bool{}
	for _, p := range q.GetPrefetch() {
		using[p.GetUsing()] = true
		require.GreaterOrEqual(t, p.GetLimit(), uint64(8), "each leg must retrieve at least top_k candidates")
	}
	require.True(t, using["dense"] && using["bm25"], "one dense leg and one sparse leg, got %v", using)
	require.Equal(t, uint64(8), q.GetLimit())
}

// Every leg must carry the request filter. A prefetch without a filter
// draws its top-N from the whole collection and only then intersects with
// root_ids/kind/mime, so scoped queries came back empty far too often.
func TestBuildHybridQuery_FilterOnEveryLeg(t *testing.T) {
	q := buildHybridQuery(hybridReq(true))
	require.NotNil(t, q.GetFilter(), "top-level filter")
	for _, p := range q.GetPrefetch() {
		require.NotNil(t, p.GetFilter(), "prefetch leg %q must be filtered", p.GetUsing())
		require.Len(t, p.GetFilter().GetMust(), len(q.GetFilter().GetMust()))
	}
}

func TestBuildHybridQuery_DenseOnlyWhenNoSparse(t *testing.T) {
	q := buildHybridQuery(hybridReq(false))
	require.Empty(t, q.GetPrefetch())
	_, ok := q.GetQuery().GetVariant().(*pb.Query_Nearest)
	require.True(t, ok, "dense-only should be a plain nearest query")
	require.Equal(t, "dense", q.GetUsing())
	require.NotNil(t, q.GetFilter())
}
