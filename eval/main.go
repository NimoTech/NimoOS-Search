// Command eval is the retrieval evaluation harness for NimoOS-Search
// (spec: nimo_os_docs/docs/superpowers/specs/2026-08-24-search-eval-and-wiki-debts-design.md §A).
//
//	go run ./eval -queries queries.json -label baseline -out reports/
//	go run ./eval -queries queries.json -label no-rerank -mode text -rerank=false -compare reports/baseline.results.json
//
// It talks to the running service directly (discovery file or -addr), judges
// each case by "does any expected path/file_id appear in the top-k", and
// writes <label>.results.json (machine-comparable) + <label>.report.md.
// The real query set lives in the private docs repo (nimo_os_docs/eval/search);
// eval/queries.example.json is a synthetic sample of the schema.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	var (
		queries = flag.String("queries", "", "query set JSON (required)")
		addr    = flag.String("addr", "", "service base URL; default: read /var/run/nimoos/search.url")
		user    = flag.String("user", "1", "X-NimoOS-User-ID to search as")
		label   = flag.String("label", time.Now().Format("20060102-150405"), "name of this run (file prefix)")
		out     = flag.String("out", ".", "output directory")
		mode    = flag.String("mode", "tool", "tool = POST /v1/search/agent/tool nimoos_search (multi-source); text = POST /v1/search/text (semantic only)")
		rerank  = flag.Bool("rerank", true, "text mode only: rerank on/off")
		topK    = flag.Int("topk", 10, "top_k requested per source")
		cmp     = flag.String("compare", "", "previous <label>.results.json to diff against")
		timeout = flag.Duration("timeout", 120*time.Second, "per-request timeout")
	)
	flag.Parse()
	if *queries == "" {
		fmt.Fprintln(os.Stderr, "-queries is required")
		os.Exit(2)
	}
	if *addr == "" {
		b, err := os.ReadFile("/var/run/nimoos/search.url")
		if err != nil {
			fmt.Fprintln(os.Stderr, "no -addr and cannot read /var/run/nimoos/search.url:", err)
			os.Exit(2)
		}
		*addr = strings.TrimSpace(string(b))
	}
	qs, err := loadQueries(*queries)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r := &runner{addr: strings.TrimRight(*addr, "/"), user: *user, mode: *mode, rerank: *rerank, topK: *topK,
		hc: &http.Client{Timeout: *timeout}}
	res := &Results{Label: *label, At: time.Now().Format(time.RFC3339), Addr: *addr, Mode: *mode, Queries: *queries}
	for i, c := range qs.Cases {
		cr := r.run(c)
		res.Cases = append(res.Cases, cr)
		fmt.Fprintf(os.Stderr, "[%d/%d] %-10s rank=%-3d %5dms %s\n", i+1, len(qs.Cases), c.ID, cr.Rank, cr.LatencyMs, cr.Err)
	}
	res.Summary = summarize(res.Cases)

	var diff *Diff
	var oldLabel string
	if *cmp != "" {
		old, err := loadResults(*cmp)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		d := compare(old, res)
		diff, oldLabel = &d, old.Label
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	jb, _ := json.MarshalIndent(res, "", "  ")
	_ = os.WriteFile(filepath.Join(*out, *label+".results.json"), jb, 0o644)
	md := renderReport(res, diff, oldLabel)
	_ = os.WriteFile(filepath.Join(*out, *label+".report.md"), []byte(md), 0o644)
	fmt.Print(md)
}

func loadQueries(path string) (*QuerySet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var qs QuerySet
	if err := json.Unmarshal(b, &qs); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(qs.Cases) == 0 {
		return nil, fmt.Errorf("%s: no cases", path)
	}
	return &qs, nil
}

func loadResults(path string) (*Results, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Results
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

type runner struct {
	addr, user, mode string
	rerank           bool
	topK             int
	hc               *http.Client
}

func (r *runner) run(c Case) CaseResult {
	cr := CaseResult{ID: c.ID, Query: c.Query}
	var body map[string]any
	var url string
	switch r.mode {
	case "text":
		url = r.addr + "/v1/search/text"
		body = map[string]any{"query": c.Query, "top_k": r.topK, "rerank": r.rerank}
	default:
		url = r.addr + "/v1/search/agent/tool"
		sources := c.Sources
		if len(sources) == 0 {
			sources = []string{"semantic", "filenames"}
		}
		body = map[string]any{"name": "nimoos_search", "arguments": map[string]any{
			"query": c.Query, "sources": sources, "top_k": r.topK}}
	}
	jb, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(jb))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NimoOS-User-ID", r.user)
	t0 := time.Now()
	resp, err := r.hc.Do(req)
	cr.LatencyMs = int(time.Since(t0).Milliseconds())
	if err != nil {
		cr.Err = err.Error()
		return cr
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		cr.Err = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(rb), 200))
		return cr
	}
	groups, warnings := parseGroups(r.mode, rb)
	cr.Warnings = warnings
	cr.Rank, cr.Source, cr.Matched = rankOf(c, groups)
	return cr
}

// parseGroups reduces either response shape to per-source candidate lists.
func parseGroups(mode string, body []byte) (map[string][]candidate, []string) {
	groups := map[string][]candidate{}
	if mode == "text" {
		var resp struct {
			Hits []struct {
				FileID string `json:"file_id"`
				Paths  []struct {
					Path string `json:"path"`
				} `json:"paths"`
			} `json:"hits"`
			Warnings []string `json:"warnings"`
		}
		_ = json.Unmarshal(body, &resp)
		for _, h := range resp.Hits {
			var ps []string
			for _, p := range h.Paths {
				ps = append(ps, p.Path)
			}
			groups["semantic"] = append(groups["semantic"], candidate{paths: ps, fileID: h.FileID})
		}
		return groups, resp.Warnings
	}
	var resp struct {
		Groups   map[string][]map[string]any `json:"groups"`
		Warnings []string                    `json:"warnings"`
	}
	_ = json.Unmarshal(body, &resp)
	for src, hits := range resp.Groups {
		for _, h := range hits {
			c := candidate{}
			if fid, ok := h["file_id"].(string); ok {
				c.fileID = fid
			}
			if p, ok := h["path"].(string); ok { // filenames / notes
				c.paths = append(c.paths, p)
			}
			if ps, ok := h["paths"].([]any); ok { // semantic
				for _, x := range ps {
					if m, ok := x.(map[string]any); ok {
						if p, ok := m["path"].(string); ok {
							c.paths = append(c.paths, p)
						}
					} else if s, ok := x.(string); ok {
						c.paths = append(c.paths, s)
					}
				}
			}
			if aid, ok := h["asset_id"].(string); ok && c.fileID == "" { // images
				c.fileID = "photos:" + aid
			}
			groups[src] = append(groups[src], c)
		}
	}
	return groups, resp.Warnings
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func renderReport(r *Results, d *Diff, oldLabel string) string {
	var b strings.Builder
	s := r.Summary
	fmt.Fprintf(&b, "# Search eval — %s\n\n", r.Label)
	fmt.Fprintf(&b, "- at: %s  \n- addr: %s  \n- mode: %s  \n- queries: %s (%d cases, %d errors)\n\n", r.At, r.Addr, r.Mode, r.Queries, s.N, s.Errors)
	fmt.Fprintf(&b, "| Recall@1 | Recall@5 | Recall@10 | MRR@10 | p50 ms | p95 ms |\n|---|---|---|---|---|---|\n")
	fmt.Fprintf(&b, "| %.3f | %.3f | %.3f | %.3f | %d | %d |\n\n", s.Recall1, s.Recall5, s.Recall10, s.MRR10, s.P50Ms, s.P95Ms)
	if d != nil {
		fmt.Fprintf(&b, "## Compared with %s\n\n", oldLabel)
		fmt.Fprintf(&b, "- gained (miss → hit): %d %v\n- lost (hit → miss): %d %v\n- moved: %d\n\n", len(d.Gained), d.Gained, len(d.Lost), d.Lost, len(d.Moved))
		if len(d.Moved) > 0 {
			fmt.Fprintf(&b, "| case | old rank | new rank |\n|---|---|---|\n")
			sort.Slice(d.Moved, func(i, j int) bool { return d.Moved[i].ID < d.Moved[j].ID })
			for _, m := range d.Moved {
				fmt.Fprintf(&b, "| %s | %d | %d |\n", m.ID, m.Old, m.New)
			}
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "## Cases\n\n| case | rank | source | latency ms | matched | warnings |\n|---|---|---|---|---|---|\n")
	for _, c := range r.Cases {
		rank := fmt.Sprint(c.Rank)
		if c.Rank == 0 {
			rank = "miss"
		}
		m := c.Matched
		if i := strings.LastIndex(m, "/"); i >= 0 {
			m = m[i+1:]
		}
		w := strings.Join(c.Warnings, ",")
		if c.Err != "" {
			w = "ERR " + truncate(c.Err, 60)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %s | %s |\n", c.ID, rank, c.Source, c.LatencyMs, truncate(m, 50), w)
	}
	return b.String()
}
