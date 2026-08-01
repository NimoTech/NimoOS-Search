# NimoOS-Search

NimoOS's **RAG retrieval API service**, aggregating three sources — semantic file-content search, filename indexing, and image search — into a single unified query entry point, while also serving as the backend for the AI Agent's `nimoos_search` / `read_file_chunk` / `read_document` tools. Current version `v1.9.0-alpha1`.

Binds to localhost, forwarded by the Gateway, API prefix `/v1/search`.

---

## Architecture Overview

```
                External request (Gateway :80 forwards /v1/search/*)
                                │
                                ▼
           ┌─────────────────────────────────────────────┐
           │  nimoos-search (Go, Echo v4, Uber FX)       │
           │  127.0.0.1:<random>                         │
           │                                             │
           │  Aggregator                                 │
           │  ┌──────────────┬──────────────┬──────────┐ │
           │  │ Semantic      │ Filename      │ Image     │ │
           │  │ search        │ index         │ search    │ │
           │  │ (SearchSvc)  │ (FileIndex)  │ (Photos) │ │
           │  └──────┬───────┴──────┬───────┴────┬─────┘ │
           └─────────┼──────────────┼────────────┼───────┘
                     │              │            │
        ┌────────────┼──────────────┤            │
        │            │              │            │
        ▼            ▼              ▼            ▼
  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────────┐
  │ Qdrant   │  │ Parser   │  │ SQLite   │  │ NimoOS-Photos  │
  │ :6333/   │  │ :8283    │  │ search   │  │ (optional,     │
  │ :6334    │  │ (embed/  │  │ .db      │  │  discovered via│
  │ (gRPC)   │  │  rerank/ │  │ filename │  │  photos.url)    │
  │ vector   │  │  expand) │  │ triple   │  └────────────────┘
  │ store    │  └──────────┘  │ index    │
  └──────────┘                └──────────┘
                     │
        ┌────────────┘
        ▼
  ┌──────────────┐
  │ NimoOS-Wiki  │
  │ wiki.url     │
  │ service      │
  │ discovery,   │
  │ direct       │
  │ connection   │
  │ /v1/wiki/    │
  │ _internal/   │
  │ user-roots   │
  └──────────────┘
        (authorization for the user's storage root directories)
```

**Data flow (semantic search)**: user query → Parser Embed (BGE-M3) → Qdrant hybrid search (dense + sparse/BM25) → Parser Rerank (BGE-Reranker-v2-m3) → Parser ExpandFiles fills in paths → return Hits.

**Data flow (filename search)**: user query → tokenize → SQLite FTS5 trigram (or LIKE fallback) → sort by match score + mtime.

**Data flow (image search)**: proxies `POST /v1/photos/search/smart`, passing through `X-NimoOS-User-ID`.

---

## API Routes (`/v1/search`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Health check |
| POST | `/v1/search/text` | Semantic search (embed → Qdrant → rerank → expand) |
| GET | `/v1/search/file` | Get all chunks of a file (by `file_id`, paginated) |
| GET | `/v1/search/chunk` | Get a specific chunk plus its context window (±window) |
| GET | `/v1/search/agent/tools` | Return the OpenAI function schema for tools available to the Agent (currently 3) |
| POST | `/v1/search/agent/tool` | Dispatch an Agent tool call (`nimoos_search` / `read_file_chunk` / `read_document`) |
| GET | `/v1/search/agent/filters-schema` | Return the JSON Schema for the filters parameter |
| POST | `/v1/search/visual` | Image semantic search (proxies NimoOS-Photos; 503 when undiscovered) |
| GET | `/v1/search/settings` | Get runtime-tunable settings |
| PUT | `/v1/search/settings` | Update settings (some fields take effect live, some require a restart) |
| POST | `/v1/search/fileindex/rescan` | Trigger a manual filename-index rescan |
| GET | `/v1/search/fileindex/status` | Filename index status + inotify quota info |
| GET | `/v1/search/_internal/health` | Internal health check (not registered with the Gateway) |
| GET | `/v1/search/_internal/stats` | Internal KPI snapshot (rerank fallback rate / embed cache hit rate) |
| POST | `/v1/search/_internal/warm` | Trigger a BGE-M3 warm-up |
| POST | `/v1/search/hybrid` | Placeholder 503 (cross-modal hybrid, pending the Parser image pipeline) |
| GET | `/v1/search/thumb/:file_id` | Placeholder 404 (thumbnail service not implemented) |

---

## Three-Way Aggregation Strategy (`Aggregator`)

`Aggregator.Aggregate()` fans out **concurrently** to three sources via `errgroup`; any source failing just degrades (writes a warning) without affecting the overall response:

| Source | Field `sources` | Backend | TopK config |
|---|---|---|---|
| Semantic vector | `"semantic"` | Qdrant `text_chunks` collection | `semantic_top_k` (default 5) |
| Filename index | `"filenames"` | SQLite `search.db` | `filename_top_k` (default 5) |
| Images | `"images"` | NimoOS-Photos `smart_search` | `image_top_k` (default 5) |

When `sources` is empty, `default_sources` is used (all three on by default). The overall cap on aggregated result count is `max_total_results` (default 15), truncated evenly across the three sources at a 1/3 ratio each.

**Agent tools** (`service/agent_tools.go`, dispatched via `Invoke()`, with the route layer injecting allowedRoots to prevent privilege escalation):

| Tool | Purpose |
|---|---|
| `nimoos_search` | Calls `Aggregator.Aggregate()`, returns `groups.{semantic, filenames, images}`; each Hit's paths are truncated to 3 entries and preview to 200 characters to protect the LLM's context window |
| `read_file_chunk` | Fetches a chunk plus its ±window context by `file_id + kind + chunk_no` (`AuthzService.GetChunkWindow`) |
| `read_document` | Reconstructs the **full text** by `file_id` (file-reader M1): `AuthzService.GetDocumentText` pulls all body chunks from Qdrant `ScrollByFileID`, concatenates them in chunk_no order, inserting a `[Page N]` marker wherever the page number changes; paginated via `offset` / `max_chars` (default 24000) params, response includes `truncated` / `total_chars` / `next_offset` |

---

## Semantic Search Pipeline (`SearchService`)

```
query
  │
  ├─ 1. EmbedCache.GetOrLoad(SHA256(query))
  │      singleflight dedupes concurrent calls → POST /v1/parser/embed (BGE-M3)
  │      returns dense(float32[]) + sparse(BM25 Indices/Values)
  │
  ├─ 2. Qdrant.SearchTextHybrid
  │      prefetch: sparse BM25 vector ("bm25" using, limit×2)
  │      query: dense vector ("dense" using)
  │      filter: root_ids / mime / kind / lang / mtime_after_ms
  │      candidates: RerankerCandidates (default 40)
  │
  ├─ 3. Rerank: POST /v1/parser/rerank (BGE-Reranker-v2-m3)
  │      on failure, falls back → keeps Qdrant raw_score, writes warnings["rerank_unavailable"]
  │
  ├─ 4. Sort by Score desc
  │      GroupByFile mode: merges by file_id, taking top_k files × max_chunks_per_file
  │
  └─ 5. ExpandFiles: GET /v1/parser/_internal/files?file_ids=
         fills in each Hit's Paths (FilePath[]{root_id, path, mtime_ms}) + mime
```

---

## Filename Index (`fileindex.Subsystem`)

SQLite table `file_index`, with optional FTS5 trigram acceleration, storing each file's path / name / ext / size / mtime_ms / root.

**Startup behavior**:
1. `PurgeOutside(roots)` — cleans up stale rows in the database that no longer belong to the current set of root directories
2. `BootScan(roots)` — full WalkDir upsert
3. `Watcher.Start()` — fsnotify listens for Create/Write/Remove/Rename events, recursively adding watches for new directories

**Live root directory hot updates**: `PUT /v1/search/settings` immediately calls `ReloadRoots()` after changing `fileindex_roots`, no service restart needed (PurgeOutside + rescan run in the background).

**Degradation mechanism**: when the number of inotify watches is exhausted (ENOSPC/EMFILE), the watcher enters degraded mode, falling back to periodic Reconcile scans at `fileindex_scan_interval_h` (normal state, default 6h) / `fileindex_degraded_scan_interval_h` (degraded state, default 1h), and sends a `Search:Warning{kind:"fileindex_watch_degraded"}` event via MessageBus.

**Skip rules**: hidden files (starting with `.`), `node_modules`, `@eaDir` (Synology-specific directory), `.git`, `__pycache__`.

**System mount point exclusion** (`Index.SetExcludes`, `service/fileindex/index.go`): the `[fileindex] Exclude` config specifies a set of absolute path subtrees; by default it excludes the NimoOS overlay-root system mounts `/media/root-ro`, `/media/root-rw`, `/mnt/overlay`, `/mnt/metadata` — otherwise scanning `/mnt` / `/media` would pull the entire OS root filesystem (hundreds of thousands of system files) into the index, saturating a full CPU core for tens of minutes on every restart/periodic scan. The exclusion set applies across BootScan / ScanInto / Reconcile and the watcher (recursive watch-adding + Create events); Reconcile also removes existing rows that fall into a newly-excluded subtree, so old indexes self-clean after an upgrade.

---

## Runtime-Tunable Settings (`SearchSettings`)

Persisted to `/var/lib/nimoos/search-settings.json` (JSON), merged with the INI config at startup; `PUT /v1/search/settings` writes atomically and switches over live.

| Field | Type | Live | Description |
|---|---|---|---|
| `default_sources` | string[] | Yes | Default aggregation sources, one or more of `semantic`/`filenames`/`images` |
| `semantic_top_k` | int [1,20] | Yes | Top-K fetched per query from the semantic source |
| `filename_top_k` | int [1,20] | Yes | Top-K for the filename source |
| `image_top_k` | int [1,20] | Yes | Top-K for the image source |
| `max_total_results` | int [1,60] | Yes | Combined cap across all three sources (truncated proportionally) |
| `fileindex_roots` | string[] | Yes (ReloadRoots) | Index root directories |
| `fileindex_enabled` | bool | No (restart required) | Whether the filename index is enabled |
| `fileindex_scan_interval_h` | int | No (restart required) | Periodic scan interval in normal state (hours) |

The `PUT` response includes a `restart_required` field telling the caller whether a service restart is needed.

---

## Authorization and User Scope

**Authorization method**: the Search service itself **does not validate JWTs**; it relies on the Gateway completing JWT validation before forwarding and injecting the `X-NimoOS-User-ID` header. The `InjectUserID` middleware reads the UID from the header and stores it in the Echo context.

**Root directory authorization**: every request calls `WikiClient.UserRoots(ctx, uid)` to get the list of root_ids that user is allowed to access (60s LRU cache), then intersects it with the request's `filters.root_ids` (`ApplyScope`). All data endpoints (text / file / chunk / agent/tool) enforce this authorization check. When the intersection is empty, returns 200 + `warnings["no_accessible_roots"]` (rather than 403, to avoid leaking whether a file_id exists).

**localhost / Unix socket exemption**: handled at the Gateway level; the Search service itself doesn't implement any exemption logic.

---

## Dependent Services

| Service | Communication | Purpose | Discovery |
|---|---|---|---|
| **NimoOS-Parser** | HTTP | embed (BGE-M3) / rerank (BGE-Reranker-v2-m3) / expand_files | `/var/run/nimoos/parser.url` |
| **Qdrant** | gRPC `:6334` | vector hybrid search (read-only), collection `text_chunks` | config `QdrantURL`/`QdrantGRPCPort` |
| **NimoOS-Wiki** | direct HTTP | fetches the user's accessible root_ids (`/v1/wiki/_internal/user-roots`) | `/var/run/nimoos/wiki.url` |
| **NimoOS-Photos** | HTTP | image semantic search proxy | `/var/run/nimoos/photos.url` (optional) |
| **NimoOS-Gateway** | HTTP | route registration (`POST /v1/gateway/routes`) | `/var/run/nimoos/management.url` |
| **NimoOS-MessageBus** | Unix socket | publishes warning/KPI events (best-effort) | `/var/run/nimoos/message-bus.sock` |

When Photos is undiscovered, the service starts normally; `/v1/search/visual` returns 503, and the aggregated `images` source is automatically skipped.

> **Wiki no longer goes through the public Gateway**: the Gateway already rejects all `/_internal/` paths (NimoOS-Gateway e2c9b9c); user-roots now reads `WikiDiscoveryPath` (`wiki.url`) to connect to the Wiki service directly, the same approach as ParserClient (`main.go` `newWikiClient`). When the discovery file is missing, the client still gets assembled, but every call fails and data endpoints degrade to 503.

---

## MessageBus Events

A background goroutine publishes a KPI snapshot every 60 seconds:

| Event | Trigger condition |
|---|---|
| `Search:Warning` | anomalies such as the embedder being unavailable or fileindex watch degradation |
| `Search:RerankFallbackRate` | periodic: rerank fallback rate |
| `Search:CacheHitRate` | periodic: embed cache hit rate |

---

## Data / Runtime Layout

```
/etc/nimoos/nimoos-search.conf      config file (INI), all defaults apply if missing
/var/lib/nimoos/db/search.db        filename index SQLite (fileindex)
/var/lib/nimoos/search-settings.json runtime-tunable settings
/var/log/nimoos/                    logs
/var/run/nimoos/search.url          service discovery address (random port, written at startup)
```

**Qdrant collections** (written by Parser, Search is read-only):

| Collection | Contents |
|---|---|
| `text_chunks` | all text-modality chunks (body / ocr / caption / transcript / summary), each with a dense + sparse vector and payload (file_id / root_ids / kind / mime / text / chunk_no / mtime_ms, etc.) |

---

## Config Reference (`/etc/nimoos/nimoos-search.conf`)

```ini
[common]
RuntimePath = /var/run/nimoos
DataPath    = /var/lib/nimoos/search
LogPath     = /var/log/nimoos

[search]
BindHost           = 127.0.0.1
RerankerEnabled    = true
RerankerCandidates = 40
DefaultTopK        = 20
EmbedCacheSize     = 1000
EmbedCacheTtlSec   = 300
UserRootsCacheTtlSec = 60
QdrantUrl          = http://127.0.0.1:6333
QdrantGrpcPort     = 6334

[upstream]
ParserDiscoveryPath  = /var/run/nimoos/parser.url
WikiDiscoveryPath    = /var/run/nimoos/wiki.url
GatewayDiscoveryPath = /var/run/nimoos/management.url
MessageBusSocket     = /var/run/nimoos/message-bus.sock

[fileindex]
Enabled            = true
Roots              = /DATA,/mnt,/media
; Subtrees never indexed/watched, defaults to NimoOS overlay-root system mounts
Exclude            = /media/root-ro,/media/root-rw,/mnt/overlay,/mnt/metadata
DBPath             = /var/lib/nimoos/db/search.db
ScanIntervalH      = 6
DegradedScanIntervalH = 1

[photos]
DiscoveryPath = /var/run/nimoos/photos.url

[aggregate]
SemanticTopK    = 5
FilenameTopK    = 5
ImageTopK       = 5
MaxTotalResults = 15
```

Every field has a code-level default, so the service starts normally even if the config file is missing. Can also be overridden via environment variables (`SEARCH_BIND_HOST`, `SEARCH_QDRANT_URL`, `SEARCH_FILEINDEX_ENABLED`, etc.).

---

## Build and Deploy

```bash
# Build (pure Go, no CGO dependency)
cd NimoOS-Search && go build -o nimoos-search .

# Test
go test ./...

# Regenerate OpenAPI code
go generate ./...   # equivalent to oapi-codegen -generate types,server,spec api/search/openapi.yaml > codegen/search_api.go

# Deploy to a running system
bash scripts/deploy.sh search
```

The systemd unit uses `Type=notify`; `SdNotify(Ready)` fires after successful registration with the Gateway.

---

## Key Module Quick Reference

| Package | Responsibility |
|---|---|
| `main.go` | Uber FX wiring, random port binding, writes `search.url`, registers with the Gateway, EventBus 60s ticker |
| `route/v1/` | Echo route registration; `middleware.go` injects User-ID; endpoint handlers |
| `service/search.go` | Five-stage semantic search pipeline (embed → Qdrant → rerank → sort → expand) |
| `service/aggregate.go` | Three-way concurrent aggregation + proportional truncation |
| `service/authz.go` | file/chunk authorization (root_id intersection check) + `GetDocumentText` full-text reconstruction |
| `service/fileindex/` | SQLite filename index: Index / Scan / Watch / Subsystem |
| `service/settings.go` | Runtime settings read/write (RWMutex + atomic file write) |
| `service/parser_client.go` | Parser HTTP client: embed / rerank / expand_files |
| `service/wiki_client.go` | Wiki HTTP client: user-roots (60s LRU cache) |
| `service/qdrant_client.go` | Qdrant gRPC client: hybrid search / scroll |
| `service/photos_client.go` | Photos HTTP proxy client: smart_search |
| `service/agent_tools.go` | Agent tool schema + `nimoos_search`/`read_file_chunk`/`read_document` dispatch |
| `service/eventbus.go` | MessageBus Unix socket best-effort publishing; KPI counters |
| `service/cache.go` | Embed LRU cache + singleflight concurrent dedup |
| `config/config.go` | INI + environment variable config loading |
| `common/version.go` | Version constant `v1.9.0-alpha1` |

---

## Known Issues and Operational Notes

1. **inotify watch quota**: large NAS setups (hundreds of thousands of directories) can easily exhaust the kernel's default 8192 watches. Once exceeded, the watcher automatically degrades to periodic scanning; `GET /v1/search/fileindex/status` returns an `inotify.raise_cmd` hint command. After deployment, it's recommended to run:
   ```bash
   sudo sysctl -w fs.inotify.max_user_watches=524288
   echo 'fs.inotify.max_user_watches=524288' | sudo tee -a /etc/sysctl.conf
   ```

2. **Startup order**: Search only marks itself `ready` after `BootScan` completes (fileindex status changes from `scanning` to `ready`); Qdrant and Parser need to be ready before Search, otherwise semantic queries return 503.

3. **PurgeOutside**: every root directory change (including at restart) cleans up rows in the database that no longer belong to the current `fileindex_roots`. If a broad path like `/mnt` is narrowed to a specific subdirectory, the old index entries get cleared and rebuilt.

4. **Photos is optional**: when the Photos service is undiscovered, the aggregated `images` source is silently skipped, without affecting the semantic / filenames sources.

5. **Model version compatibility**: embed uses `bge-m3`, rerank uses `bge-reranker-v2-m3`, both running inside the NimoOS-Parser process, with versions managed by Parser. Search only passes `ParserVersion:"parser/0.1.0"` as a log marker.
