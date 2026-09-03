# Search evaluation harness

`go run ./eval -queries <queries.json> -label <name> -out <dir>` runs every case
against the live service and writes `<name>.results.json` (machine-comparable)
and `<name>.report.md` (Recall@1/5/10, MRR@10, latency p50/p95, per-case table).

- `-mode tool` (default): `POST /v1/search/agent/tool` `nimoos_search`, honouring each
  case's `sources` (default `semantic`,`filenames`). A case is a hit at the best rank
  where any `expect` matches in any source group.
- `-mode text [-rerank=false]`: `POST /v1/search/text`, semantic only — the knob to
  prove a deliberate degradation is caught.
- `-compare old.results.json`: adds a gained / lost / moved table to the report.
- `-addr` overrides the discovery file `/var/run/nimoos/search.url`; `-user` sets `X-NimoOS-User-ID`.

Judging: `path_contains` is a case-insensitive substring of any hit path; `file_id`
is an exact match. `queries.example.json` documents the schema. The real query
set and reports live in the private docs repo (`nimo_os_docs/eval/search/`) so the
open-source tree never leaks corpus paths. This directory is a standalone
`package main`: it is not part of the service binary or the release.
