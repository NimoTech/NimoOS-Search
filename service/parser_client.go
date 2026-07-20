package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	ErrEmbedderUnavailable = errors.New("parser embedder unavailable")
	ErrRerankerUnavailable = errors.New("parser reranker unavailable")
	ErrParserUnavailable   = errors.New("parser unavailable")
)

type ParserClient struct {
	src *BaseURLSource
	hc  *http.Client
}

func NewParserClient(src *BaseURLSource, timeoutSec int) *ParserClient {
	return &ParserClient{
		src: src,
		hc:  &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
	}
}

// ---- Embed ----

type Sparse struct {
	Indices []int     `json:"indices"`
	Values  []float32 `json:"values"`
}

type EmbedResult struct {
	Dense        []float32 `json:"dense"`
	Sparse       *Sparse   `json:"sparse,omitempty"`
	Dim          int       `json:"dim"`
	ModelVersion string    `json:"model_version"`
}

func (c *ParserClient) Embed(ctx context.Context, model, inputType, text, imageB64 string) (*EmbedResult, error) {
	body := map[string]any{"model": model, "input_type": inputType}
	if text != "" {
		body["text"] = text
	}
	if imageB64 != "" {
		body["image_b64"] = imageB64
	}
	buf, _ := json.Marshal(body)
	resp, err := doWithRediscover(c.hc, c.src, func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/v1/parser/embed", bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbedderUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 503 {
		return nil, ErrEmbedderUnavailable
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("parser embed %d: %s", resp.StatusCode, string(b))
	}
	var out EmbedResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- Rerank ----

type RerankCandidate struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type RerankScore struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

type RerankResult struct {
	Scores       []RerankScore `json:"scores"`
	ModelVersion string        `json:"model_version"`
	TookMs       int           `json:"took_ms"`
}

func (c *ParserClient) Rerank(ctx context.Context, query string, candidates []RerankCandidate, topK *int) (*RerankResult, error) {
	body := map[string]any{
		"model":      "bge-reranker-v2-m3",
		"query":      query,
		"candidates": candidates,
	}
	if topK != nil {
		body["top_k"] = *topK
	}
	buf, _ := json.Marshal(body)
	resp, err := doWithRediscover(c.hc, c.src, func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			base+"/v1/parser/rerank", bytes.NewReader(buf))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRerankerUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 503 {
		return nil, ErrRerankerUnavailable
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("parser rerank %d: %s", resp.StatusCode, string(b))
	}
	var out RerankResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- ExpandFiles (T7) ----

type FilePath struct {
	RootID  string `json:"root_id"`
	Path    string `json:"path"`
	MtimeMs int64  `json:"mtime_ms"`
}

type FileRecord struct {
	FileID         string            `json:"file_id"`
	Paths          []FilePath        `json:"paths"`
	Mime           string            `json:"mime"`
	ModalitiesDone map[string]string `json:"modalities_done"`
	ParserVersion  string            `json:"parser_version"`
	IndexedAt      int64             `json:"indexed_at"`
	TombstonedAt   *int64            `json:"tombstoned_at,omitempty"`
}

type ExpandFilesResult struct {
	Files   []FileRecord `json:"files"`
	Missing []string     `json:"missing"`
}

func (c *ParserClient) ExpandFiles(ctx context.Context, fileIDs []string) (*ExpandFilesResult, error) {
	if len(fileIDs) == 0 {
		return &ExpandFilesResult{}, nil
	}
	q := ""
	for i, id := range fileIDs {
		if i > 0 {
			q += ","
		}
		q += id
	}
	resp, err := doWithRediscover(c.hc, c.src, func(base string) (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet,
			base+"/v1/parser/_internal/files?file_ids="+q, nil)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParserUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("parser expand_files %d: %s", resp.StatusCode, string(b))
	}
	var out ExpandFilesResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
