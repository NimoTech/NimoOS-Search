package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

// mimeFacetTTL bounds how stale the "which mimes exist in the collection"
// snapshot may be. New mime values only appear when Parser indexes a file of
// a never-seen-before format, so a minute of staleness is invisible in
// practice, while the cache spares Qdrant one facet per search.
const mimeFacetTTL = 60 * time.Second

// mimeFacetCache is the memoised facet over payload.mime in text_chunks.
type mimeFacetCache struct {
	mu     sync.Mutex
	values []string
	exp    time.Time
}

// knownMimes returns every distinct payload.mime present in the text
// collection, cached for mimeFacetTTL.
func (s *SearchService) knownMimes(ctx context.Context) ([]string, error) {
	s.mimeCache.mu.Lock()
	defer s.mimeCache.mu.Unlock()
	if s.mimeCache.values != nil && time.Now().Before(s.mimeCache.exp) {
		return s.mimeCache.values, nil
	}
	vals, err := s.Qdrant.DistinctValues(ctx, collectionTextChunks, "mime")
	if err != nil {
		return nil, err
	}
	if vals == nil {
		vals = []string{}
	}
	s.mimeCache.values = vals
	s.mimeCache.exp = time.Now().Add(mimeFacetTTL)
	return vals, nil
}

// isMimePrefix reports whether a mime_prefix entry is a prefix (ends in "/")
// as opposed to an exact mime. The trailing slash is the whole contract: it
// lets "text/" mean "every text type" while "text/markdown" keeps meaning
// exactly markdown and never swallows "text/markdown+docling/pdf".
func isMimePrefix(entry string) bool {
	return strings.HasSuffix(entry, "/")
}

func hasMimePrefixEntries(entries []string) bool {
	for _, e := range entries {
		if isMimePrefix(e) {
			return true
		}
	}
	return false
}

// expandMimePrefixes turns the public mime_prefix list into the exact mime
// set Qdrant can match: exact entries pass through untouched (even when the
// collection has never seen them — they simply match nothing), prefixes
// expand to every known mime that starts with them. Order is preserved
// (caller order, then facet order) and duplicates are dropped.
func expandMimePrefixes(entries, known []string) []string {
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	add := func(v string) {
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, e := range entries {
		if !isMimePrefix(e) {
			add(e)
			continue
		}
		for _, k := range known {
			if strings.HasPrefix(k, e) {
				add(k)
			}
		}
	}
	return out
}
