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

// CaptionSource 点查单张照片的 caption 文本,满足者为 *AuthzService(它已经
// 持有 Qdrant 并复用 ScrollByFileID 的授权语义)。放在 aggregate.go 而非
// authz.go 声明,是为了让 Aggregator 的依赖面板一眼看全——images 分支要用
// 到的接口都在这一个文件里。
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
	// Captions 是可选的 caption 点查源;nil 时整段附着逻辑跳过(兼容测试里
	// 不关心 caption 的既有构造惯例)。非 nil 时对 images 命中逐张附着,
	// 单张失败/无命中一律 fail-open(留空继续),绝不影响命中数或产生新
	// warning——caption 只是锦上添花的证据,不是 images 组能否返回的前提。
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
			// 逐张附着 caption 文本,让 agent/UI 拿到的 images 命中不只是文件
			// 名——否则 LLM 手里只有路径没有文字证据,容易误判"没找到"。
			// fail-open:Captions==nil 整段跳过(旧行为);单张查询出错或没
			// 有 caption(空字符串、nil error)都只是让那一张 Caption 留空
			// 继续,绝不能让 caption 查询的可用性拖累 images 组本身——因此
			// 这里刻意不设置 imgWarn,也不提前 return。
			if a.Captions != nil {
				for i := range h {
					c, cerr := a.Captions.PhotoCaption(ctx, h[i].AssetID, req.AllowedRoots)
					if cerr != nil {
						continue
					}
					// 截断放在这里(而不是只信赖 CaptionSource 实现自己截断),
					// 是为了让 aggregate 层对任何 CaptionSource 实现(包括测试
					// 用的 fake)都有统一、可断言的长度上限。
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
