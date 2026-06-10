# NimoOS-Search

NimoOS 的 **RAG 检索 API 服务**,把文件内容语义检索、文件名索引、图片搜索三路来源聚合为统一的查询入口,同时作为 AI Agent 的 search/read_file_chunk 工具后端。当前版本 `v1.9.0-alpha1`。

绑定 localhost、由 Gateway 转发,API 前缀 `/v1/search`。

---

## 架构概览

```
                外部请求(Gateway :80 转发 /v1/search/*)
                                │
                                ▼
           ┌─────────────────────────────────────────────┐
           │  nimoos-search (Go, Echo v4, Uber FX)       │
           │  127.0.0.1:<random>                         │
           │                                             │
           │  Aggregator                                 │
           │  ┌──────────────┬──────────────┬──────────┐ │
           │  │ 语义检索      │ 文件名索引    │ 图片搜索  │ │
           │  │ (SearchSvc)  │ (FileIndex)  │ (Photos) │ │
           │  └──────┬───────┴──────┬───────┴────┬─────┘ │
           └─────────┼──────────────┼────────────┼───────┘
                     │              │            │
        ┌────────────┼──────────────┤            │
        │            │              │            │
        ▼            ▼              ▼            ▼
  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌────────────────┐
  │ Qdrant   │  │ Parser   │  │ SQLite   │  │ NimoOS-Photos  │
  │ :6333/   │  │ :8283    │  │ search   │  │ (可选,按        │
  │ :6334    │  │ (embed/  │  │ .db      │  │  photos.url     │
  │ (gRPC)   │  │  rerank/ │  │ 文件名    │  │  发现)          │
  │ 向量库    │  │  expand) │  │ 三元组索引│  └────────────────┘
  └──────────┘  └──────────┘  └──────────┘
                     │
        ┌────────────┘
        ▼
  ┌──────────────┐
  │ NimoOS-Wiki  │
  │ Gateway:80   │
  │ /v1/wiki/    │
  │ _internal/   │
  │ user-roots   │
  └──────────────┘
        (用户存储根目录鉴权)
```

**数据流(语义检索)**:用户 query → Parser Embed (BGE-M3) → Qdrant 混合搜索 (dense + sparse/BM25) → Parser Rerank (BGE-Reranker-v2-m3) → Parser ExpandFiles 补全路径 → 返回 Hits。

**数据流(文件名检索)**:用户 query → 分词 → SQLite FTS5 trigram(或 LIKE 降级)→ 按匹配分 + mtime 排序。

**数据流(图片检索)**:代理 `POST /v1/photos/search/smart`,携带 `X-NimoOS-User-ID` 透传。

---

## API 路由(`/v1/search`)

| Method | Path | 用途 |
|---|---|---|
| GET | `/healthz` | 健康检查 |
| POST | `/v1/search/text` | 语义检索(embed → Qdrant → rerank → expand) |
| GET | `/v1/search/file` | 获取文件全部 chunks(按 `file_id`,分页) |
| GET | `/v1/search/chunk` | 获取指定 chunk 及上下文窗口(±window) |
| GET | `/v1/search/agent/tools` | 返回 Agent 可用工具的 OpenAI function schema |
| POST | `/v1/search/agent/tool` | 分发 Agent 工具调用(`nimoos_search` / `read_file_chunk`) |
| GET | `/v1/search/agent/filters-schema` | 返回 filters 参数的 JSON Schema |
| POST | `/v1/search/visual` | 图片语义搜索(代理 NimoOS-Photos;未发现时 503) |
| GET | `/v1/search/settings` | 获取运行时可调设置 |
| PUT | `/v1/search/settings` | 更新设置(部分字段热生效,部分需重启) |
| POST | `/v1/search/fileindex/rescan` | 触发文件名索引手动重扫 |
| GET | `/v1/search/fileindex/status` | 文件名索引状态 + inotify 配额信息 |
| GET | `/v1/search/_internal/health` | 内部健康检查(不注册至 Gateway) |
| GET | `/v1/search/_internal/stats` | 内部 KPI 快照(rerank 降级率 / embed cache 命中率) |
| POST | `/v1/search/_internal/warm` | 触发 BGE-M3 预热 |
| POST | `/v1/search/hybrid` | 占位 503(跨模态 hybrid,等 Parser 图像 pipeline) |
| GET | `/v1/search/thumb/:file_id` | 占位 404(缩略图服务未实现) |

---

## 三路聚合策略(`Aggregator`)

`Aggregator.Aggregate()` 通过 `errgroup` **并发**扇出到三个来源,任意来源失败只降级(写 warnings),不影响整体响应:

| 来源 | 字段 `sources` | 读取后端 | TopK 配置 |
|---|---|---|---|
| 语义向量 | `"semantic"` | Qdrant `text_chunks` 集合 | `semantic_top_k`(默认 5) |
| 文件名索引 | `"filenames"` | SQLite `search.db` | `filename_top_k`(默认 5) |
| 图片 | `"images"` | NimoOS-Photos `smart_search` | `image_top_k`(默认 5) |

`sources` 为空时使用 `default_sources`(默认三路全开)。聚合结果总条目上限由 `max_total_results`(默认 15)按 1/3 比例均等截断。

**Agent 工具 `nimoos_search`**:调用 `Aggregator.Aggregate()`,返回 `groups.{semantic, filenames, images}`,每个 Hit 截断路径至 3 条、preview 至 200 字符,保护 LLM 上下文窗口。

---

## 语义检索流水线(`SearchService`)

```
query
  │
  ├─ 1. EmbedCache.GetOrLoad(SHA256(query))
  │      singleflight 并发去重 → POST /v1/parser/embed (BGE-M3)
  │      返回 dense(float32[]) + sparse(BM25 Indices/Values)
  │
  ├─ 2. Qdrant.SearchTextHybrid
  │      prefetch: sparse BM25 向量("bm25" using, limit×2)
  │      query: dense 向量("dense" using)
  │      filter: root_ids / mime / kind / lang / mtime_after_ms
  │      candidates: RerankerCandidates(默认 40)
  │
  ├─ 3. Rerank: POST /v1/parser/rerank (BGE-Reranker-v2-m3)
  │      失败时 fallback → 保留 Qdrant raw_score,写 warnings["rerank_unavailable"]
  │
  ├─ 4. Sort by Score desc
  │      GroupByFile 模式:按 file_id 归并,取 top_k 个文件 × max_chunks_per_file
  │
  └─ 5. ExpandFiles: GET /v1/parser/_internal/files?file_ids=
         补全每个 Hit 的 Paths(FilePath[]{root_id, path, mtime_ms}) + mime
```

---

## 文件名索引(`fileindex.Subsystem`)

SQLite 表 `file_index`,可选 FTS5 trigram 加速,存储文件的 path / name / ext / size / mtime_ms / root。

**启动行为**:
1. `PurgeOutside(roots)` — 清理数据库中不属于当前根目录集的旧记录
2. `BootScan(roots)` — 全量 WalkDir upsert
3. `Watcher.Start()` — fsnotify 监听 Create/Write/Remove/Rename 事件,对新目录递归添加 watch

**实时根目录热更新**:`PUT /v1/search/settings` 修改 `fileindex_roots` 后立即调用 `ReloadRoots()`,无需重启服务(PurgeOutside + 重新扫描在后台执行)。

**降级机制**:inotify watch 数量耗尽时(ENOSPC/EMFILE),watcher 进入降级模式,退化为按 `fileindex_scan_interval_h`(正常态,默认 6h)/ `fileindex_degraded_scan_interval_h`(降级态,默认 1h)定期 Reconcile 扫描,并通过 MessageBus 发送 `Search:Warning{kind:"fileindex_watch_degraded"}` 事件。

**跳过规则**:隐藏文件(`.` 开头)、`node_modules`、`@eaDir`(Synology 专有目录)、`.git`、`__pycache__`。

---

## 运行时可调设置(`SearchSettings`)

持久化至 `/var/lib/nimoos/search-settings.json`(JSON),启动时与 INI 配置合并,`PUT /v1/search/settings` 原子写入后热切换。

| 字段 | 类型 | 热生效 | 说明 |
|---|---|---|---|
| `default_sources` | string[] | 是 | 聚合默认来源,可选 `semantic`/`filenames`/`images` |
| `semantic_top_k` | int [1,20] | 是 | 语义来源每次取的 Top-K |
| `filename_top_k` | int [1,20] | 是 | 文件名来源 Top-K |
| `image_top_k` | int [1,20] | 是 | 图片来源 Top-K |
| `max_total_results` | int [1,60] | 是 | 三路合计上限(等比例截断) |
| `fileindex_roots` | string[] | 是(ReloadRoots) | 索引根目录 |
| `fileindex_enabled` | bool | 否(需重启) | 是否启用文件名索引 |
| `fileindex_scan_interval_h` | int | 否(需重启) | 正常态周期扫描间隔(小时) |

`PUT` 响应中包含 `restart_required` 字段,告知调用方是否需要重启服务。

---

## 鉴权与用户范围

**鉴权方式**:Search 服务自身**不校验 JWT**,依赖 Gateway 在转发前完成 JWT 验证并注入 `X-NimoOS-User-ID` Header。`InjectUserID` 中间件从 Header 读取 UID 存入 Echo context。

**根目录授权**:每个请求调用 `WikiClient.UserRoots(ctx, uid)` 获取该用户被允许访问的 root_id 列表(60s LRU 缓存),再与请求中的 `filters.root_ids` 取交集(`ApplyScope`)。所有数据端点(text / file / chunk / agent/tool)都执行此授权检查。交集为空时返回 200 + `warnings["no_accessible_roots"]`(而非 403,避免泄露 file_id 存在性)。

**localhost / Unix socket 豁免**:由 Gateway 层面处理,Search 服务本身不实现豁免逻辑。

---

## 依赖服务

| 服务 | 通信方式 | 用途 | 发现方式 |
|---|---|---|---|
| **NimoOS-Parser** | HTTP | embed(BGE-M3)/ rerank(BGE-Reranker-v2-m3)/ expand_files | `/var/run/nimoos/parser.url` |
| **Qdrant** | gRPC `:6334` | 向量混合检索(只读),集合 `text_chunks` | 配置 `QdrantURL`/`QdrantGRPCPort` |
| **NimoOS-Wiki** | HTTP via Gateway `:80` | 获取用户可访问 root_ids | 固定 `http://127.0.0.1` |
| **NimoOS-Photos** | HTTP | 图片语义搜索代理 | `/var/run/nimoos/photos.url`(可选) |
| **NimoOS-Gateway** | HTTP | 路由注册 (`POST /v1/gateway/routes`) | `/var/run/nimoos/management.url` |
| **NimoOS-MessageBus** | Unix socket | 发布警告/KPI 事件(best-effort) | `/var/run/nimoos/message-bus.sock` |

Photos 未发现时服务正常启动,`/v1/search/visual` 返回 503,聚合的 `images` 来源自动跳过。

---

## MessageBus 事件

每 60 秒由后台 goroutine 发布一次 KPI 快照:

| 事件 | 触发条件 |
|---|---|
| `Search:Warning` | embedder 不可用、fileindex watch 降级等异常 |
| `Search:RerankFallbackRate` | 定期:rerank 失败降级比率 |
| `Search:CacheHitRate` | 定期:embed cache 命中率 |

---

## 数据/运行时布局

```
/etc/nimoos/nimoos-search.conf      配置文件(INI),缺失时全用默认值
/var/lib/nimoos/db/search.db        文件名索引 SQLite(fileindex)
/var/lib/nimoos/search-settings.json 运行时可调设置
/var/log/nimoos/                    日志
/var/run/nimoos/search.url          服务发现地址(随机端口,启动时写入)
```

**Qdrant 集合**(由 Parser 写入,Search 只读):

| 集合 | 内容 |
|---|---|
| `text_chunks` | 所有文本模态 chunk(body / ocr / caption / transcript / summary),每条含 dense + sparse 向量、payload(file_id / root_ids / kind / mime / text / chunk_no / mtime_ms 等) |

---

## 配置参考(`/etc/nimoos/nimoos-search.conf`)

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

所有字段均有代码默认值,配置文件缺失时服务正常启动。也可用环境变量覆盖(`SEARCH_BIND_HOST`、`SEARCH_QDRANT_URL`、`SEARCH_FILEINDEX_ENABLED` 等)。

---

## 构建与部署

```bash
# 构建(纯 Go,无 CGO 依赖)
cd NimoOS-Search && go build -o nimoos-search .

# 测试
go test ./...

# 重新生成 OpenAPI 代码
go generate ./...   # 等价于 oapi-codegen -generate types,server,spec api/search/openapi.yaml > codegen/search_api.go

# 部署到运行中的系统
bash nimo_os_docs/scripts/deploy.sh search
```

systemd 单元使用 `Type=notify`,`SdNotify(Ready)` 在 Gateway 注册成功后发出。

---

## 关键模块速查

| 包 | 职责 |
|---|---|
| `main.go` | Uber FX 组装、随机端口绑定、写 `search.url`、向 Gateway 注册、EventBus 60s ticker |
| `route/v1/` | Echo 路由注册;`middleware.go` 注入 User-ID;各端点处理器 |
| `service/search.go` | 语义检索五段流水线(embed → Qdrant → rerank → sort → expand) |
| `service/aggregate.go` | 三路并发聚合 + 比例截断 |
| `service/authz.go` | file/chunk 鉴权(root_id 交集校验) |
| `service/fileindex/` | SQLite 文件名索引:Index / Scan / Watch / Subsystem |
| `service/settings.go` | 运行时设置读写(RWMutex + 原子文件写入) |
| `service/parser_client.go` | Parser HTTP 客户端:embed / rerank / expand_files |
| `service/wiki_client.go` | Wiki HTTP 客户端:user-roots(60s LRU 缓存) |
| `service/qdrant_client.go` | Qdrant gRPC 客户端:hybrid search / scroll |
| `service/photos_client.go` | Photos HTTP 代理客户端:smart_search |
| `service/agent_tools.go` | Agent tool schema + `nimoos_search`/`read_file_chunk` dispatch |
| `service/eventbus.go` | MessageBus Unix socket best-effort 发布;KPI 计数器 |
| `service/cache.go` | Embed LRU 缓存 + singleflight 并发去重 |
| `config/config.go` | INI + 环境变量配置加载 |
| `common/version.go` | 版本常量 `v1.9.0-alpha1` |

---

## 已知问题与运维要点

1. **inotify watch 配额**:大型 NAS(数十万目录)易耗尽内核默认 8192 个 watches。超出后 watcher 自动降级为定期扫描,`GET /v1/search/fileindex/status` 返回 `inotify.raise_cmd` 提示命令。部署后建议执行:
   ```bash
   sudo sysctl -w fs.inotify.max_user_watches=524288
   echo 'fs.inotify.max_user_watches=524288' | sudo tee -a /etc/sysctl.conf
   ```

2. **启动顺序**:Search 在 `BootScan` 完成后才标记 `ready`(fileindex 状态从 `scanning` 变为 `ready`);Qdrant 和 Parser 需在 Search 之前就绪,否则语义查询返回 503。

3. **PurgeOutside**:每次根目录变更(含重启时)都会清理数据库中不属于当前 `fileindex_roots` 的记录。若将 `/mnt` 之类宽泛路径改为具体子目录,旧索引会被清除并重建。

4. **Photos 可选**:Photos 服务未发现时,聚合的 `images` 来源静默跳过,不影响 semantic / filenames 两路。

5. **模型版本兼容**:embed 使用 `bge-m3`,rerank 使用 `bge-reranker-v2-m3`,均运行在 NimoOS-Parser 进程中,版本由 Parser 管理。Search 仅传递 `ParserVersion:"parser/0.1.0"` 作为日志标记。
