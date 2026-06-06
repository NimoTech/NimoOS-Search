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
}

// PhotosClient proxies NimoOS-Photos POST /v1/photos/search/smart. Photos is a
// single shared library today (no per-user filter); we pass X-NimoOS-User-ID
// through so it works the day Photos adds per-user scoping (spec §5/§10).
type PhotosClient struct {
	base string
	http *http.Client
}

func NewPhotosClient(base string, timeoutSec int) *PhotosClient {
	if timeoutSec <= 0 {
		timeoutSec = 5
	}
	return &PhotosClient{base: base, http: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}}
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/photos/search/smart", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.Header.Set("X-NimoOS-User-ID", userID)
	}
	resp, err := c.http.Do(req)
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
