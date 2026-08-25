package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	pb "github.com/qdrant/go-client/qdrant"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type QdrantClient struct {
	conn   *grpc.ClientConn
	points pb.PointsClient
	// httpBase is Qdrant's REST endpoint (e.g. http://127.0.0.1:6333). Only
	// the facet API goes over REST: the pinned go-client (v1.10) predates
	// gRPC Facet, and bumping it would drag the module past the go 1.21 pin.
	httpBase string
	hc       *http.Client
}

func NewQdrantClient(host string, grpcPort int, httpBase string) (*QdrantClient, error) {
	addr := fmt.Sprintf("%s:%d", host, grpcPort)
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &QdrantClient{
		conn: conn, points: pb.NewPointsClient(conn),
		httpBase: strings.TrimRight(httpBase, "/"),
		hc:       &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// DistinctValues lists the distinct values of a keyword payload field via
// Qdrant's facet API (REST: POST /collections/{name}/facet, Qdrant >= 1.12).
// exact=true asks for precise counts; we only use the values.
func (c *QdrantClient) DistinctValues(ctx context.Context, collection, key string) ([]string, error) {
	if c.httpBase == "" {
		return nil, errors.New("qdrant: no REST base url configured")
	}
	body, _ := json.Marshal(map[string]any{"key": key, "limit": 1000, "exact": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.httpBase+"/collections/"+url.PathEscape(collection)+"/facet", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("qdrant facet %s/%s: status %d", collection, key, resp.StatusCode)
	}
	var out struct {
		Result struct {
			Hits []struct {
				Value any `json:"value"`
			} `json:"hits"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	vals := make([]string, 0, len(out.Result.Hits))
	for _, h := range out.Result.Hits {
		if s, ok := h.Value.(string); ok {
			vals = append(vals, s)
		}
	}
	return vals, nil
}

func (c *QdrantClient) Close() error { return c.conn.Close() }

type QdrantFilter struct {
	RootIDsAny []string
	FileIDsAny []string
	// MimeIn is a set of EXACT mime values. The public filter is called
	// mime_prefix, but Qdrant's keyword index cannot prefix-match, so
	// SearchService expands any prefix into the exact values present in the
	// collection (see mime_prefix.go) before it reaches this struct.
	MimeIn       []string
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
	if len(f.MimeIn) > 0 {
		must = append(must, matchKeywordAny("mime", f.MimeIn))
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
