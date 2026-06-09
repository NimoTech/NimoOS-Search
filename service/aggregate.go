package service

import (
	"context"

	"github.com/NimoTech/NimoOS-Search/service/fileindex"
	"golang.org/x/sync/errgroup"
)

// FileNameSearcher is satisfied by *fileindex.Index (nil if disabled).
type FileNameSearcher interface {
	Search(ctx context.Context, query string, topK int) ([]fileindex.FileNameHit, error)
	Status() string
}

// ImageSearcher is satisfied by *PhotosClient (nil if Photos undiscovered).
type ImageSearcher interface {
	SmartSearch(ctx context.Context, query string, topK int, userID string) ([]ImageHit, error)
}

type AggregateRequest struct {
	Query        string
	Sources      []string // empty → falls back to SettingsStore.DefaultSources
	Filters      *Filters
	AllowedRoots []string
	UserID       string
}

type AggregateGroups struct {
	Semantic  []any                   `json:"semantic"`
	Filenames []fileindex.FileNameHit `json:"filenames"`
	Images    []ImageHit              `json:"images"`
}

type AggregateResponse struct {
	Groups   AggregateGroups `json:"groups"`
	Stats    map[string]any  `json:"stats"`
	Warnings []string        `json:"warnings"`
}

type Aggregator struct {
	Search    *SearchService
	FileIndex FileNameSearcher
	Photos    ImageSearcher
	Settings  *SettingsStore
}

func wants(sources []string, name string) bool {
	if len(sources) == 0 {
		return true
	}
	for _, s := range sources {
		if s == name {
			return true
		}
	}
	return false
}

// Aggregate fans out to the sources resolved from req.Sources (or DefaultSources
// when empty) concurrently. A failed source degrades to an empty group plus a
// warning; it never fails the whole call.
func (a *Aggregator) Aggregate(ctx context.Context, req AggregateRequest) *AggregateResponse {
	st := a.Settings.Get()
	sources := req.Sources
	if len(sources) == 0 {
		sources = st.DefaultSources // empty → fall back to configured default
	}
	resp := &AggregateResponse{Stats: map[string]any{}}
	var (
		semHits  []any
		semWarn  string
		fileHits []fileindex.FileNameHit
		fileWarn string
		imgHits  []ImageHit
		imgWarn  string
	)
	g := new(errgroup.Group)
	if wants(sources, "semantic") && a.Search != nil {
		g.Go(func() error {
			scoped, warn := ApplyScope(req.Filters, req.AllowedRoots)
			if warn == "no_accessible_roots" {
				semWarn = warn
				return nil
			}
			r, err := a.Search.SearchText(ctx, SearchRequest{Query: req.Query, Filters: scoped, TopK: st.SemanticTopK, Rerank: true})
			if err != nil {
				semWarn = "semantic_unavailable"
				return nil
			}
			semHits = trimHits(r)
			return nil
		})
	}
	if wants(sources, "filenames") && a.FileIndex != nil {
		g.Go(func() error {
			h, err := a.FileIndex.Search(ctx, req.Query, st.FilenameTopK)
			if err != nil {
				fileWarn = "filenames_unavailable"
				return nil
			}
			fileHits = h
			return nil
		})
	}
	if wants(sources, "images") && a.Photos != nil {
		g.Go(func() error {
			h, err := a.Photos.SmartSearch(ctx, req.Query, st.ImageTopK, req.UserID)
			if err != nil {
				imgWarn = "images_unavailable"
				return nil
			}
			imgHits = h
			return nil
		})
	}
	_ = g.Wait()
	resp.Groups.Semantic = semHits
	resp.Groups.Filenames = fileHits
	resp.Groups.Images = imgHits
	for _, w := range []string{semWarn, fileWarn, imgWarn} {
		if w != "" {
			resp.Warnings = append(resp.Warnings, w)
		}
	}
	applyTotalCap(resp, st.MaxTotalResults)
	status := "ready"
	if a.FileIndex != nil {
		status = a.FileIndex.Status()
	}
	resp.Stats["fileindex_status"] = status
	resp.Stats["total_candidates"] = len(resp.Groups.Semantic) + len(resp.Groups.Filenames) + len(resp.Groups.Images)
	return resp
}

// applyTotalCap trims groups proportionally so the combined count never exceeds
// max (protects LLM context).
func applyTotalCap(resp *AggregateResponse, max int) {
	if max <= 0 {
		return
	}
	total := len(resp.Groups.Semantic) + len(resp.Groups.Filenames) + len(resp.Groups.Images)
	if total <= max {
		return
	}
	per := max / 3
	if per < 1 {
		per = 1
	}
	if len(resp.Groups.Semantic) > per {
		resp.Groups.Semantic = resp.Groups.Semantic[:per]
	}
	if len(resp.Groups.Filenames) > per {
		resp.Groups.Filenames = resp.Groups.Filenames[:per]
	}
	if len(resp.Groups.Images) > per {
		resp.Groups.Images = resp.Groups.Images[:per]
	}
}
