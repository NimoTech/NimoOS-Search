package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// PhotoAsset is the subset of NimoOS-Photos' GET /v1/photos/assets/{id}
// response the search service needs to give a photos:<asset_id> hit a path.
type PhotoAsset struct {
	ID           string     `json:"id"`
	FilePath     string     `json:"filePath"`
	MimeType     string     `json:"mimeType"`
	OriginalName string     `json:"originalName"`
	TakenAt      *time.Time `json:"takenAt,omitempty"`
	DurationMs   int64      `json:"durationMs,omitempty"`
}

// ErrPhotoNotFound is returned by GetAsset for a 404: the asset was removed
// from the library after its caption was indexed. Callers treat it as a
// per-asset miss, not as Photos being unavailable.
var ErrPhotoNotFound = errors.New("photo asset not found")

// PhotoAssetLookup is the SearchService's minimal dependency on Photos (tests
// use a fake). *PhotosClient satisfies it.
type PhotoAssetLookup interface {
	GetAsset(ctx context.Context, assetID, userID string) (*PhotoAsset, error)
}

// GetAsset fetches one asset's metadata from Photos (GET
// /v1/photos/assets/{id}). Photos scopes the lookup by X-NimoOS-User-ID.
func (c *PhotosClient) GetAsset(ctx context.Context, assetID, userID string) (*PhotoAsset, error) {
	resp, err := doWithRediscover(c.http, c.src, func(base string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/photos/assets/"+url.PathEscape(assetID), nil)
		if err != nil {
			return nil, err
		}
		if userID != "" {
			req.Header.Set("X-NimoOS-User-ID", userID)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrPhotoNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("photos asset %s returned %d", assetID, resp.StatusCode)
	}
	var a PhotoAsset
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}
