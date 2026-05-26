package service

// Filters mirrors the JSON shape from OpenAPI components.Filters.
type Filters struct {
	RootIDs      []string `json:"root_ids,omitempty"`
	MimePrefix   []string `json:"mime_prefix,omitempty"`
	KindIn       []string `json:"kind_in,omitempty"`
	LangIn       []string `json:"lang_in,omitempty"`
	MtimeAfterMs int64    `json:"mtime_after_ms,omitempty"`
}

// IntersectRoots returns user-requested ∩ allowed.
// If userRequested is nil/empty, returns the full allowed set (default scope).
func IntersectRoots(userRequested, allowed []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, r := range allowed {
		allowedSet[r] = struct{}{}
	}
	if len(userRequested) == 0 {
		out := make([]string, 0, len(allowed))
		for _, r := range allowed {
			out = append(out, r)
		}
		return out
	}
	out := make([]string, 0, len(userRequested))
	for _, r := range userRequested {
		if _, ok := allowedSet[r]; ok {
			out = append(out, r)
		}
	}
	return out
}

// ApplyScope intersects RootIDs with allowed, mutates f in place, and returns
// (f, warning). warning == "no_accessible_roots" when the intersection is empty;
// callers should short-circuit to hits=[] in that case.
func ApplyScope(f *Filters, allowedRoots []string) (*Filters, string) {
	if f == nil {
		f = &Filters{}
	}
	f.RootIDs = IntersectRoots(f.RootIDs, allowedRoots)
	if len(f.RootIDs) == 0 {
		return f, "no_accessible_roots"
	}
	return f, ""
}
