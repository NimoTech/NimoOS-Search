package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ImageHit is one image candidate in the aggregated response.
type ImageHit struct {
	AssetID      string  `json:"asset_id"`
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	Score        float64 `json:"score"`
	TakenAt      string  `json:"taken_at,omitempty"`
	ThumbnailURL string  `json:"thumbnail_url"`
	// Caption is the photos caption text attached by the aggregate layer
	// (from text_chunks kind=="caption"); may be empty (caption never
	// generated, or lookup failed, fail-open). It lives here rather than on
	// a separate field path because the UI card rendering and the agent
	// tool result both go through the same ImageHit JSON, so adding one
	// field benefits both sides at once.
	Caption string `json:"caption,omitempty"`
}

// PhotosClient proxies NimoOS-Photos POST /v1/photos/search/smart. Photos is a
// single shared library today (no per-user filter); we pass X-NimoOS-User-ID
// through so it works the day Photos adds per-user scoping (spec §5/§10).
type PhotosClient struct {
	src  *BaseURLSource
	http *http.Client
}

func NewPhotosClient(src *BaseURLSource, timeoutSec int) *PhotosClient {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	return &PhotosClient{src: src, http: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}}
}

type photoAsset struct {
	ID           string  `json:"id"`
	OriginalName string  `json:"originalName"`
	FilePath     string  `json:"filePath"`
	MatchScore   float64 `json:"matchScore"`
	TakenAt      string  `json:"takenAt"`
}

func (c *PhotosClient) SmartSearch(ctx context.Context, query string, topK int, userID string) ([]ImageHit, error) {
	body, _ := json.Marshal(map[string]any{"query": query, "limit": topK})
	resp, err := doWithRediscover(c.http, c.src, func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/photos/search/smart", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if userID != "" {
			req.Header.Set("X-NimoOS-User-ID", userID)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("photos search returned %d", resp.StatusCode)
	}
	var assets []photoAsset
	if err := json.NewDecoder(resp.Body).Decode(&assets); err != nil {
		return nil, err
	}
	hits := make([]ImageHit, 0, len(assets))
	for _, a := range assets {
		hits = append(hits, ImageHit{
			AssetID:      a.ID,
			Name:         a.OriginalName,
			Path:         a.FilePath,
			Score:        a.MatchScore,
			TakenAt:      a.TakenAt,
			ThumbnailURL: fmt.Sprintf("/v1/photos/assets/%s/thumbnail?size=small", a.ID),
		})
	}
	return hits, nil
}
