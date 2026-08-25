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

// NotesSearcher is satisfied by *NotesClient (nil if Parser undiscovered).
type NotesSearcher interface {
	Query(ctx context.Context, query string, topK int, userID string) ([]NoteHit, error)
}

// CaptionSource looks up the caption text for a single photo; satisfied by
// *AuthzService (which already holds Qdrant and reuses ScrollByFileID's
// authorization semantics). Declared in aggregate.go rather than authz.go so
// the Aggregator's dependency surface is visible at a glance - every
// interface the images branch needs lives in this one file.
type CaptionSource interface {
	PhotoCaption(ctx context.Context, assetID string, allowedRoots []string) (string, error)
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
	Notes     []NoteHit               `json:"notes"`
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
	Notes     NotesSearcher
	Settings  *SettingsStore
	// Captions is an optional caption lookup source; when nil, the whole
	// attachment logic is skipped (compatible with existing test construction
	// that doesn't care about captions). When non-nil, it attaches a caption
	// to each images hit, one at a time; a per-item failure/miss is always
	// fail-open (leave it blank and continue), never affecting the hit count
	// or producing a new warning - the caption is just a nice-to-have piece
	// of evidence, not a precondition for the images group to return.
	Captions CaptionSource
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
		noteHits []NoteHit
		noteWarn string
	)
	g := new(errgroup.Group)
	if wants(sources, "semantic") && a.Search != nil {
		g.Go(func() error {
			scoped, warn := ApplyScope(req.Filters, req.AllowedRoots)
			if warn == "no_accessible_roots" {
				semWarn = warn
				return nil
			}
			r, err := a.Search.SearchText(ctx, SearchRequest{Query: req.Query, Filters: scoped, TopK: st.SemanticTopK, Rerank: true, UserID: req.UserID})
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
			// Attach caption text to each item so the images hits the
			// agent/UI gets aren't just filenames - otherwise the LLM only
			// has a path with no textual evidence, and easily misjudges it
			// as "not found". Fail-open: Captions==nil skips the whole
			// block (old behavior); a per-item query error or missing
			// caption (empty string, nil error) just leaves that item's
			// Caption blank and continues - the availability of caption
			// lookups must never drag down the images group itself, so we
			// deliberately don't set imgWarn here, nor return early.
			if a.Captions != nil {
				for i := range h {
					c, cerr := a.Captions.PhotoCaption(ctx, h[i].AssetID, req.AllowedRoots)
					if cerr != nil {
						continue
					}
					// Truncation happens here (rather than relying solely on
					// the CaptionSource implementation to truncate) so the
					// aggregate layer has a uniform, assertable length cap
					// for any CaptionSource implementation, including test
					// fakes.
					h[i].Caption = truncateRunes(c, 200)
				}
			}
			imgHits = h
			return nil
		})
	}
	if wants(sources, "notes") && a.Notes != nil {
		g.Go(func() error {
			h, err := a.Notes.Query(ctx, req.Query, st.NotesTopK, req.UserID)
			if err != nil {
				noteWarn = "notes_unavailable"
				return nil
			}
			noteHits = h
			return nil
		})
	}
	_ = g.Wait()
	resp.Groups.Semantic = semHits
	resp.Groups.Filenames = fileHits
	resp.Groups.Images = imgHits
	resp.Groups.Notes = noteHits
	for _, w := range []string{semWarn, fileWarn, imgWarn, noteWarn} {
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
	resp.Stats["total_candidates"] = len(resp.Groups.Semantic) + len(resp.Groups.Filenames) + len(resp.Groups.Images) + len(resp.Groups.Notes)
	return resp
}

// applyTotalCap trims groups proportionally so the combined count never exceeds
// max (protects LLM context).
func applyTotalCap(resp *AggregateResponse, max int) {
	if max <= 0 {
		return
	}
	total := len(resp.Groups.Semantic) + len(resp.Groups.Filenames) + len(resp.Groups.Images) + len(resp.Groups.Notes)
	if total <= max {
		return
	}
	per := max / 4
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
	if len(resp.Groups.Notes) > per {
		resp.Groups.Notes = resp.Groups.Notes[:per]
	}
}
