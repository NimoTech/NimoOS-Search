//go:generate bash -c "mkdir -p codegen && go run github.com/deepmap/oapi-codegen/cmd/oapi-codegen@v1.12.4 -generate types,server,spec -package codegen api/search/openapi.yaml > codegen/search_api.go"

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	nurl "net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/NimoTech/NimoOS-Common/middleware"
	"github.com/NimoTech/NimoOS-Search/common"
	"github.com/NimoTech/NimoOS-Search/config"
	v1 "github.com/NimoTech/NimoOS-Search/route/v1"
	"github.com/NimoTech/NimoOS-Search/service"
	"github.com/NimoTech/NimoOS-Search/service/fileindex"
	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/labstack/echo/v4"
	"go.uber.org/fx"
)

var (
	commit = "private build"
	date   = "private build"
)

func main() {
	configFlag := flag.String("c", "", "config file path")
	versionFlag := flag.Bool("v", false, "version")
	flag.Parse()
	if *versionFlag {
		fmt.Printf("v%s\n", common.Version)
		os.Exit(0)
	}
	fmt.Println("git commit:", commit, "build date:", date)

	confPath := *configFlag
	if confPath == "" {
		confPath = filepath.Join("/etc/nimoos", common.SearchName+"."+common.SearchConfigType)
	}

	cfg, err := config.Load(confPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config load:", err)
		os.Exit(1)
	}

	app := fx.New(
		fx.Supply(cfg),
		fx.Provide(
			newParserClient,
			newNimoOSClient,
			newQdrantClient,
			newCache,
			newSearchService,
			newSettingsStore,
			newFileIndex,
			newPhotosClient,
			newNotesClient,
			newAggregator,
			newAuthzService,
			newAgentTools,
			newEcho,
			newEventBus,
		),
		fx.Invoke(registerRoutes, startListener, func(*service.EventBus) {}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "fx start:", err)
		os.Exit(1)
	}
	_, _ = daemon.SdNotify(false, daemon.SdNotifyReady)

	// Block on SIGTERM/SIGINT
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_, _ = daemon.SdNotify(false, daemon.SdNotifyStopping)
	_ = app.Stop(stopCtx)
}

// ---- fx providers ----

func newParserClient(cfg config.Config) (*service.ParserClient, error) {
	src := service.NewBaseURLSource(cfg.ParserDiscoveryPath, "http://127.0.0.1:8283")
	return service.NewParserClient(src, cfg.ParserTimeoutSec), nil
}

func newNimoOSClient(cfg config.Config) (*service.NimoOSClient, error) {
	// The authorization source moved from Wiki to core (the main NimoOS
	// service, see Task 8). The Gateway rejects all /_internal/ paths
	// (NimoOS-Gateway e2c9b9c), so root authorization queries still have to
	// go through the discovery file directly, same as ParserClient: if the
	// file is missing the client still gets assembled, it just fails on
	// every call and routes still degrade to 503 as before. The client holds
	// a BaseURLSource internally, so once core restarts on a different port,
	// a transport error triggers a re-read of the discovery file plus a
	// retry - Search doesn't have to restart itself to self-heal.
	src := service.NewBaseURLSource(cfg.NimoOSDiscoveryPath, "http://127.0.0.1")
	return service.NewNimoOSClient(src,
		time.Duration(cfg.UserRootsCacheTTLSec)*time.Second), nil
}

func newQdrantClient(cfg config.Config, lc fx.Lifecycle) (*service.QdrantClient, error) {
	host := "127.0.0.1"
	if u, err := parseHostFromURL(cfg.QdrantURL); err == nil && u != "" {
		host = u
	}
	c, err := service.NewQdrantClient(host, cfg.QdrantGRPCPort)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(ctx context.Context) error { return c.Close() }})
	return c, nil
}

func newCache(cfg config.Config) *service.EmbedCache {
	return service.NewEmbedCache(cfg.EmbedCacheSize, time.Duration(cfg.EmbedCacheTTLSec)*time.Second)
}

func newSearchService(p *service.ParserClient, q *service.QdrantClient, ca *service.EmbedCache, cfg config.Config) *service.SearchService {
	return &service.SearchService{
		Parser: p, Qdrant: q, Cache: ca,
		ParserVersion:      "parser/0.1.0",
		DefaultTopK:        cfg.DefaultTopK,
		RerankerCandidates: cfg.RerankerCandidates,
	}
}

func newAuthzService(q *service.QdrantClient) *service.AuthzService {
	return &service.AuthzService{Qdrant: q}
}

func newSettingsStore(cfg config.Config) (*service.SettingsStore, error) {
	return service.LoadSettingsStore(cfg.SettingsPath, service.SearchSettings{
		SchemaVersion:          service.SettingsSchemaVersion,
		DefaultSources:         []string{"semantic", "filenames", "images", "notes"},
		SemanticTopK:           cfg.AggSemanticTopK,
		FilenameTopK:           cfg.AggFilenameTopK,
		ImageTopK:              cfg.AggImageTopK,
		NotesTopK:              cfg.AggNotesTopK,
		MaxTotalResults:        cfg.AggMaxTotalResults,
		FileIndexEnabled:       cfg.FileIndexEnabled,
		FileIndexRoots:         cfg.FileIndexRoots,
		FileIndexScanIntervalH: cfg.FileIndexScanIntervalH,
	})
}

func newFileIndex(cfg config.Config, st *service.SettingsStore, eb *service.EventBus, lc fx.Lifecycle) (*fileindex.Subsystem, error) {
	s := st.Get()
	if !s.FileIndexEnabled {
		return &fileindex.Subsystem{}, nil // disabled: nil Index → Report()=disabled
	}
	if err := os.MkdirAll(filepath.Dir(cfg.FileIndexDBPath), 0755); err != nil {
		return nil, err
	}
	idx, err := fileindex.Open(cfg.FileIndexDBPath)
	if err != nil {
		return nil, err
	}
	idx.SetExcludes(cfg.FileIndexExclude) // never index NimoOS system mounts under /mnt, /media
	idx.ThrottleEvery = cfg.FileIndexWalkThrottleEvery
	idx.ThrottleSleep = time.Duration(cfg.FileIndexWalkThrottleSleepMs) * time.Millisecond
	normal := time.Duration(s.FileIndexScanIntervalH) * time.Hour
	if normal <= 0 {
		normal = 6 * time.Hour
	}
	degraded := time.Duration(cfg.FileIndexDegradedScanIntervalH) * time.Hour
	if degraded <= 0 {
		degraded = time.Hour
	}
	sub := fileindex.NewSubsystem(idx, normal, degraded, func() {
		eb.PublishWarning("fileindex_watch_degraded",
			"inotify watch limit reached; falling back to periodic scan. Raise fs.inotify.max_user_watches.")
	})
	sub.StartInitial(s.FileIndexRoots) // PurgeOutside(roots)+BootScan(roots) in background
	lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
		return sub.Close()
	}})
	return sub, nil
}

func newPhotosClient(cfg config.Config) (*service.PhotosClient, error) {
	// Always wire the client over a BaseURLSource, same as NimoOS/Parser: if
	// Photos hasn't been discovered yet (file unreadable), Get() falls back
	// to "" and calls degrade to per-request errors, but every subsequent
	// call re-reads the file while it has never resolved — so a Photos
	// instance starting AFTER Search self-heals instead of requiring a
	// Search restart. A boot-time nil-gate here would strand that case.
	src := service.NewBaseURLSource(cfg.PhotosDiscoveryPath, "")
	return service.NewPhotosClient(src, 5), nil
}

func newNotesClient(cfg config.Config) (*service.NotesClient, error) {
	// Same discovery source as ParserClient; see newPhotosClient for why the
	// boot-time nil-gate is gone.
	src := service.NewBaseURLSource(cfg.ParserDiscoveryPath, "")
	return service.NewNotesClient(src, 5), nil
}

func newAggregator(s *service.SearchService, sub *fileindex.Subsystem, p *service.PhotosClient, n *service.NotesClient, st *service.SettingsStore, az *service.AuthzService) *service.Aggregator {
	agg := &service.Aggregator{Search: s, Settings: st}
	// Assign through interfaces only when non-nil, so a nil *T doesn't become a
	// non-nil interface (Go typed-nil trap) that breaks the `== nil` checks.
	if sub != nil && sub.Index != nil {
		agg.FileIndex = sub.Index
	}
	if p != nil {
		agg.Photos = p
	}
	if n != nil {
		agg.Notes = n
	}
	if az != nil {
		// AuthzService already holds Qdrant and implements PhotoCaption (see
		// authz.go), so just reuse it instead of building a dedicated
		// caption client.
		agg.Captions = az
	}
	return agg
}

func newAgentTools(agg *service.Aggregator, a *service.AuthzService) *service.AgentTools {
	return &service.AgentTools{Agg: agg, Authz: a}
}

func newEcho() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.Use(v1.InjectUserID)
	return e
}

func newEventBus(cfg config.Config, lc fx.Lifecycle) *service.EventBus {
	eb := service.NewEventBus(cfg.MessageBusSocket)
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(60 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				eb.PublishStatsSnapshot()
			}
		}
	}()
	lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
		close(stop)
		return nil
	}})
	return eb
}

func registerRoutes(e *echo.Echo, s *service.SearchService, a *service.AuthzService,
	n *service.NimoOSClient, t *service.AgentTools, p *service.PhotosClient,
	sub *fileindex.Subsystem, st *service.SettingsStore) {
	deps := &v1.Deps{Search: s, Authz: a, NimoOS: n, Tools: t, Settings: st, FileIndex: sub}
	if p != nil {
		deps.Photos = p
	}
	e.GET("/healthz", v1.Healthz)
	middleware.RegisterVersionRoute(e, "/v1/search/version", "Search", common.Version)
	v1.RegisterText(e, deps)
	v1.RegisterFile(e, deps)
	v1.RegisterChunk(e, deps)
	v1.RegisterAgent(e, deps)
	v1.RegisterVisual(e, deps)
	v1.RegisterStubs(e)
	v1.RegisterInternal(e, deps)
	v1.RegisterSettings(e, deps)
}

func startListener(lc fx.Lifecycle, e *echo.Echo, cfg config.Config) error {
	ln, err := net.Listen("tcp", cfg.BindHost+":0")
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	fmt.Println("search listening on", addr)
	// Write discovery file
	urlPath := filepath.Join(cfg.RuntimePath, "search.url")
	_ = os.MkdirAll(cfg.RuntimePath, 0755)
	_ = os.WriteFile(urlPath, []byte("http://"+addr+"\n"), 0644)
	srv := &http.Server{Handler: e}
	go func() { _ = srv.Serve(ln) }()
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			_ = os.Remove(urlPath)
			return srv.Shutdown(ctx)
		},
	})
	// Gateway registration
	gwBase, gerr := readDiscoveryURL(cfg.GatewayDiscoveryPath, "")
	if gerr == nil && gwBase != "" {
		if err := service.RegisterAtGateway(gwBase, "http://"+addr, "/v1/search"); err != nil {
			fmt.Println("WARN: gateway register failed:", err)
		}
	}
	return nil
}

// ---- helpers ----

func readDiscoveryURL(path, fallback string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return "", err
	}
	return string(trimNewline(b)), nil
}

func trimNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b
}

// parseHostFromURL extracts host portion of "http://host:port"; returns ""
// if the URL is malformed (caller falls back).
func parseHostFromURL(s string) (string, error) {
	u, err := nurl.Parse(s)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if host == "" {
		return "", nil
	}
	return host, nil
}
