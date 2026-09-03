package main

import (
	"sort"
	"strings"
)

// Case is one evaluation query. Expect entries are alternatives: the case
// counts as hit at the first rank where any of them matches.
type Case struct {
	ID      string   `json:"id"`
	Query   string   `json:"query"`
	Sources []string `json:"sources,omitempty"`
	Expect  []Expect `json:"expect"`
	Note    string   `json:"note,omitempty"`
}

type Expect struct {
	PathContains string `json:"path_contains,omitempty"` // case-insensitive substring of any hit path
	FileID       string `json:"file_id,omitempty"`       // exact (case-insensitive) file_id
}

type QuerySet struct {
	Version int    `json:"version"`
	Corpus  string `json:"corpus,omitempty"`
	Cases   []Case `json:"cases"`
}

// candidate is one ranked hit from one source, reduced to what the judge needs.
type candidate struct {
	paths  []string
	fileID string
}

type CaseResult struct {
	ID        string   `json:"id"`
	Query     string   `json:"query"`
	Rank      int      `json:"rank"` // 1-based best rank across sources; 0 = miss
	Source    string   `json:"source,omitempty"`
	Matched   string   `json:"matched,omitempty"`
	LatencyMs int      `json:"latency_ms"`
	Err       string   `json:"err,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

type Summary struct {
	N        int     `json:"n"`
	Recall1  float64 `json:"recall_at_1"`
	Recall5  float64 `json:"recall_at_5"`
	Recall10 float64 `json:"recall_at_10"`
	MRR10    float64 `json:"mrr_at_10"`
	P50Ms    int     `json:"latency_p50_ms"`
	P95Ms    int     `json:"latency_p95_ms"`
	Errors   int     `json:"errors"`
}

type Results struct {
	Label   string       `json:"label"`
	At      string       `json:"at"`
	Addr    string       `json:"addr"`
	Mode    string       `json:"mode"`
	Queries string       `json:"queries"`
	Summary Summary      `json:"summary"`
	Cases   []CaseResult `json:"cases"`
}

func (e Expect) matches(c candidate) (string, bool) {
	if e.PathContains != "" {
		needle := strings.ToLower(e.PathContains)
		for _, p := range c.paths {
			if strings.Contains(strings.ToLower(p), needle) {
				return p, true
			}
		}
	}
	if e.FileID != "" && strings.EqualFold(e.FileID, c.fileID) {
		return c.fileID, true
	}
	return "", false
}

// rankOf returns the best 1-based rank at which any expectation of c appears
// in any source group, with the source and the matched path/id. 0 = miss.
func rankOf(c Case, groups map[string][]candidate) (rank int, source, matched string) {
	srcs := make([]string, 0, len(groups))
	for s := range groups {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs) // deterministic tie-break
	for _, s := range srcs {
		for i, cand := range groups[s] {
			if rank != 0 && i+1 >= rank {
				break
			}
			for _, e := range c.Expect {
				if m, ok := e.matches(cand); ok {
					rank, source, matched = i+1, s, m
					break
				}
			}
		}
	}
	return rank, source, matched
}

func percentile(sorted []int, p int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted)*p+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func summarize(rs []CaseResult) Summary {
	s := Summary{N: len(rs)}
	if len(rs) == 0 {
		return s
	}
	lat := make([]int, 0, len(rs))
	var r1, r5, r10, mrr float64
	for _, r := range rs {
		if r.Err != "" {
			s.Errors++
		}
		lat = append(lat, r.LatencyMs)
		if r.Rank >= 1 && r.Rank <= 1 {
			r1++
		}
		if r.Rank >= 1 && r.Rank <= 5 {
			r5++
		}
		if r.Rank >= 1 && r.Rank <= 10 {
			r10++
			mrr += 1.0 / float64(r.Rank)
		}
	}
	n := float64(len(rs))
	s.Recall1, s.Recall5, s.Recall10, s.MRR10 = r1/n, r5/n, r10/n, mrr/n
	sort.Ints(lat)
	s.P50Ms, s.P95Ms = percentile(lat, 50), percentile(lat, 95)
	return s
}

type Move struct {
	ID  string `json:"id"`
	Old int    `json:"old"`
	New int    `json:"new"`
}

type Diff struct {
	Gained []string `json:"gained"` // miss → hit
	Lost   []string `json:"lost"`   // hit → miss
	Moved  []Move   `json:"moved"`  // hit → hit at a different rank
}

func compare(old, cur *Results) Diff {
	prev := map[string]int{}
	for _, c := range old.Cases {
		prev[c.ID] = c.Rank
	}
	var d Diff
	for _, c := range cur.Cases {
		o, ok := prev[c.ID]
		if !ok {
			continue
		}
		switch {
		case o == 0 && c.Rank != 0:
			d.Gained = append(d.Gained, c.ID)
		case o != 0 && c.Rank == 0:
			d.Lost = append(d.Lost, c.ID)
		case o != c.Rank:
			d.Moved = append(d.Moved, Move{ID: c.ID, Old: o, New: c.Rank})
		}
	}
	return d
}
