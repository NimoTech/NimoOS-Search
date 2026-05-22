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

	"github.com/NimoTech/NimoOS-Search/common"
	"github.com/NimoTech/NimoOS-Search/config"
	v1 "github.com/NimoTech/NimoOS-Search/route/v1"
	"github.com/NimoTech/NimoOS-Search/service"
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
			newWikiClient,
			newQdrantClient,
			newCache,
			newSearchService,
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
	base, err := readDiscoveryURL(cfg.ParserDiscoveryPath, "http://127.0.0.1:8283")
	if err != nil {
		return nil, err
	}
	return service.NewParserClient(base, cfg.ParserTimeoutSec), nil
}

func newWikiClient(cfg config.Config) (*service.WikiClient, error) {
	// Per spec §6.1: clients go through Gateway. We resolve via gateway.url.
	base, err := readDiscoveryURL(cfg.GatewayDiscoveryPath, "http://127.0.0.1")
	if err != nil {
		return nil, err
	}
	return service.NewWikiClient(base, cfg.WikiTimeoutSec,
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

func newAgentTools(s *service.SearchService, a *service.AuthzService) *service.AgentTools {
	return &service.AgentTools{Search: s, Authz: a}
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
	w *service.WikiClient, t *service.AgentTools) {
	deps := &v1.Deps{Search: s, Authz: a, Wiki: w, Tools: t}
	e.GET("/healthz", v1.Healthz)
	v1.RegisterText(e, deps)
	v1.RegisterFile(e, deps)
	v1.RegisterChunk(e, deps)
	v1.RegisterAgent(e, deps)
	v1.RegisterStubs(e)
	v1.RegisterInternal(e, deps)
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
