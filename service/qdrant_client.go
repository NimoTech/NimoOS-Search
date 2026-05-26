package service

import (
	"context"
	"fmt"

	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type QdrantClient struct {
	conn   *grpc.ClientConn
	points pb.PointsClient
}

func NewQdrantClient(host string, grpcPort int) (*QdrantClient, error) {
	addr := fmt.Sprintf("%s:%d", host, grpcPort)
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &QdrantClient{conn: conn, points: pb.NewPointsClient(conn)}, nil
}

func (c *QdrantClient) Close() error { return c.conn.Close() }

type QdrantFilter struct {
	RootIDsAny   []string
	FileIDsAny   []string
	MimePrefix   []string
	KindIn       []string
	LangIn       []string
	MtimeAfterMs int64
}

type QdrantHit struct {
	PointID string
	Score   float32
	Payload map[string]any
}

type QdrantSearchRequest struct {
	Collection string
	Dense      []float32
	Sparse     *Sparse
	Filter     *QdrantFilter
	Limit      int
}

// SearchTextHybrid runs Qdrant hybrid search (dense + sparse via prefetch).
// MVP uses a simple weighted RRF fuse via Qdrant's native query API.
func (c *QdrantClient) SearchTextHybrid(ctx context.Context, req QdrantSearchRequest) ([]QdrantHit, error) {
	queryReq := &pb.QueryPoints{
		CollectionName: req.Collection,
		Limit:          uint64ptr(uint64(req.Limit)),
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
		Filter:         buildPBFilter(req.Filter),
	}
	if req.Sparse != nil {
		// Prefetch sparse then rerank with dense.
		queryReq.Prefetch = []*pb.PrefetchQuery{{
			Query: &pb.Query{Variant: &pb.Query_Nearest{Nearest: &pb.VectorInput{
				Variant: &pb.VectorInput_Sparse{Sparse: &pb.SparseVector{
					Indices: uint32SliceFromInt(req.Sparse.Indices),
					Values:  req.Sparse.Values,
				}}}}},
			Using: stringPtr("bm25"),
			Limit: uint64ptr(uint64(req.Limit * 2)),
		}}
	}
	queryReq.Query = &pb.Query{Variant: &pb.Query_Nearest{Nearest: &pb.VectorInput{
		Variant: &pb.VectorInput_Dense{Dense: &pb.DenseVector{Data: req.Dense}}}}}
	queryReq.Using = stringPtr("dense")
	resp, err := c.points.Query(ctx, queryReq)
	if err != nil {
		return nil, err
	}
	hits := make([]QdrantHit, 0, len(resp.Result))
	for _, p := range resp.Result {
		hits = append(hits, QdrantHit{
			PointID: p.GetId().GetUuid(),
			Score:   p.GetScore(),
			Payload: pbPayloadToMap(p.GetPayload()),
		})
	}
	return hits, nil
}

// ScrollByFileID returns all chunks for a file_id (paginated by Qdrant scroll).
func (c *QdrantClient) ScrollByFileID(ctx context.Context, collection, fileID string,
	allowedRoots []string, limit int, offsetKey string) ([]QdrantHit, string, error) {
	filter := buildPBFilter(&QdrantFilter{
		FileIDsAny: []string{fileID},
		RootIDsAny: allowedRoots,
	})
	req := &pb.ScrollPoints{
		CollectionName: collection,
		Filter:         filter,
		Limit:          uint32ptr(uint32(limit)),
		WithPayload:    &pb.WithPayloadSelector{SelectorOptions: &pb.WithPayloadSelector_Enable{Enable: true}},
	}
	if offsetKey != "" {
		req.Offset = &pb.PointId{PointIdOptions: &pb.PointId_Uuid{Uuid: offsetKey}}
	}
	resp, err := c.points.Scroll(ctx, req)
	if err != nil {
		return nil, "", err
	}
	hits := make([]QdrantHit, 0, len(resp.Result))
	for _, p := range resp.Result {
		hits = append(hits, QdrantHit{
			PointID: p.GetId().GetUuid(),
			Payload: pbPayloadToMap(p.GetPayload()),
		})
	}
	next := ""
	if resp.NextPageOffset != nil {
		next = resp.NextPageOffset.GetUuid()
	}
	return hits, next, nil
}

func (c *QdrantClient) Count(ctx context.Context, collection string) (uint64, error) {
	resp, err := c.points.Count(ctx, &pb.CountPoints{CollectionName: collection})
	if err != nil {
		return 0, err
	}
	return resp.GetResult().GetCount(), nil
}

// ---- helpers ----

func buildPBFilter(f *QdrantFilter) *pb.Filter {
	if f == nil {
		return nil
	}
	must := []*pb.Condition{}
	if len(f.FileIDsAny) > 0 {
		must = append(must, matchKeywordAny("file_id", f.FileIDsAny))
	}
	if len(f.RootIDsAny) > 0 {
		must = append(must, matchKeywordAny("root_ids", f.RootIDsAny))
	}
	if len(f.KindIn) > 0 {
		must = append(must, matchKeywordAny("kind", f.KindIn))
	}
	if len(f.LangIn) > 0 {
		must = append(must, matchKeywordAny("lang", f.LangIn))
	}
	// mime_prefix is handled differently: Qdrant doesn't have prefix match,
	// so we expand to MatchText. MVP: pass as Any() over the full mime since
	// our chunks use exact mime ("text/markdown", etc.)
	if len(f.MimePrefix) > 0 {
		must = append(must, matchKeywordAny("mime", f.MimePrefix))
	}
	if f.MtimeAfterMs > 0 {
		gte := float64(f.MtimeAfterMs)
		must = append(must, &pb.Condition{
			ConditionOneOf: &pb.Condition_Field{
				Field: &pb.FieldCondition{
					Key:   "mtime_ms",
					Range: &pb.Range{Gte: &gte},
				},
			},
		})
	}
	return &pb.Filter{Must: must}
}

func matchKeywordAny(key string, values []string) *pb.Condition {
	return &pb.Condition{ConditionOneOf: &pb.Condition_Field{Field: &pb.FieldCondition{
		Key:   key,
		Match: &pb.Match{MatchValue: &pb.Match_Keywords{Keywords: &pb.RepeatedStrings{Strings: values}}},
	}}}
}

func pbPayloadToMap(p map[string]*pb.Value) map[string]any {
	out := make(map[string]any, len(p))
	for k, v := range p {
		out[k] = pbValueToGo(v)
	}
	return out
}

func pbValueToGo(v *pb.Value) any {
	switch x := v.Kind.(type) {
	case *pb.Value_StringValue:
		return x.StringValue
	case *pb.Value_IntegerValue:
		return x.IntegerValue
	case *pb.Value_DoubleValue:
		return x.DoubleValue
	case *pb.Value_BoolValue:
		return x.BoolValue
	case *pb.Value_ListValue:
		arr := make([]any, 0, len(x.ListValue.Values))
		for _, e := range x.ListValue.Values {
			arr = append(arr, pbValueToGo(e))
		}
		return arr
	case *pb.Value_StructValue:
		return pbPayloadToMap(x.StructValue.Fields)
	}
	return nil
}

func uint64ptr(n uint64) *uint64 { return &n }
func uint32ptr(n uint32) *uint32 { return &n }
func stringPtr(s string) *string { return &s }
func uint32SliceFromInt(in []int) []uint32 {
	out := make([]uint32, len(in))
	for i, v := range in {
		out[i] = uint32(v)
	}
	return out
}
