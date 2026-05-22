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

func main() {
	vFlag := flag.Bool("v", false, "print version and exit")
	cfgPath := flag.String("config", "/etc/nimoos/search.ini", "path to INI config file")
	flag.Parse()

	if *vFlag {
		fmt.Printf("v%s\n", common.Version)
		os.Exit(0)
	}

	app := fx.New(
		fx.Provide(
			func() (config.Config, error) { return config.Load(*cfgPath) },
			newEcho,
			newParserClient,
			newWikiClient,
			newQdrantClient,
			newEmbedCache,
			newDeps,
		),
		fx.Invoke(registerRoutes),
		fx.Invoke(runServer),
	)
	app.Run()
}

// ── providers ────────────────────────────────────────────────────────────────

func newEcho(cfg config.Config) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(v1.InjectUserID)
	return e
}

func newParserClient(cfg config.Config) (*service.ParserClient, error) {
	baseURL, err := readDiscoveryURL(cfg.ParserDiscoveryPath)
	if err != nil {
		// Non-fatal at startup: parser may come up after us. Return a client
		// pointed at a placeholder; requests will fail at call-time.
		baseURL = "http://127.0.0.1:0"
	}
	return service.NewParserClient(baseURL, cfg.ParserTimeoutSec), nil
}

func newWikiClient(cfg config.Config) (*service.WikiClient, error) {
	baseURL, err := readDiscoveryURL(cfg.WikiDiscoveryPath)
	if err != nil {
		baseURL = "http://127.0.0.1:0"
	}
	ttl := time.Duration(cfg.UserRootsCacheTTLSec) * time.Second
	return service.NewWikiClient(baseURL, cfg.WikiTimeoutSec, ttl), nil
}

func newQdrantClient(cfg config.Config) (*service.QdrantClient, error) {
	u, err := nurl.Parse(cfg.QdrantURL)
	if err != nil {
		return nil, fmt.Errorf("invalid QdrantURL %q: %w", cfg.QdrantURL, err)
	}
	host := u.Hostname()
	if host == "" {
		host = "127.0.0.1"
	}
	return service.NewQdrantClient(host, cfg.QdrantGRPCPort)
}

func newEmbedCache(cfg config.Config) *service.EmbedCache {
	ttl := time.Duration(cfg.EmbedCacheTTLSec) * time.Second
	return service.NewEmbedCache(cfg.EmbedCacheSize, ttl)
}

func newDeps(
	cfg config.Config,
	parser *service.ParserClient,
	wiki *service.WikiClient,
	qdrant *service.QdrantClient,
	cache *service.EmbedCache,
) *v1.Deps {
	svc := &service.SearchService{
		Parser:             parser,
		Qdrant:             qdrant,
		Cache:              cache,
		DefaultTopK:        cfg.DefaultTopK,
		RerankerCandidates: cfg.RerankerCandidates,
	}
	authz := &service.AuthzService{Qdrant: qdrant}
	tools := &service.AgentTools{Search: svc, Authz: authz}
	return &v1.Deps{
		Search: svc,
		Authz:  authz,
		Wiki:   wiki,
		Tools:  tools,
	}
}

// ── invokers ─────────────────────────────────────────────────────────────────

func registerRoutes(e *echo.Echo, d *v1.Deps) {
	e.GET("/healthz", v1.Healthz)
	v1.RegisterText(e, d)
	v1.RegisterFile(e, d)
	v1.RegisterChunk(e, d)
	v1.RegisterAgent(e, d)
	v1.RegisterInternal(e, d)
	v1.RegisterStubs(e)
}

func runServer(lc fx.Lifecycle, cfg config.Config, e *echo.Echo, qdrant *service.QdrantClient) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			ln, err := net.Listen("tcp", cfg.BindHost+":0")
			if err != nil {
				return fmt.Errorf("nimoos-search: listen: %w", err)
			}
			addr := ln.Addr().String()

			// Write discovery file so Gateway and other services can find us.
			urlFile := filepath.Join(cfg.RuntimePath, "search.url")
			if err := os.MkdirAll(cfg.RuntimePath, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(urlFile, []byte("http://"+addr+"\n"), 0o644); err != nil {
				return err
			}

			go func() {
				srv := &http.Server{Handler: e}
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					fmt.Fprintf(os.Stderr, "nimoos-search: %v\n", err)
				}
			}()

			// Notify systemd we are ready (no-op if not running under systemd).
			daemon.SdNotify(false, daemon.SdNotifyReady)
			fmt.Printf("nimoos-search v%s listening on %s\n", common.Version, addr)
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := qdrant.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "nimoos-search: qdrant close: %v\n", err)
			}
			return nil
		},
	})

	// Handle OS signals for graceful shutdown outside of fx's own signal
	// handling (fx handles SIGINT/SIGTERM internally; this is belt-and-suspenders).
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
	}()
}

// ── helpers ───────────────────────────────────────────────────────────────────

// readDiscoveryURL reads a NimoOS service-discovery file (one URL per line,
// trailing newline OK) and returns the trimmed URL string.
func readDiscoveryURL(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	raw := string(b)
	for len(raw) > 0 && (raw[len(raw)-1] == '\n' || raw[len(raw)-1] == '\r') {
		raw = raw[:len(raw)-1]
	}
	if raw == "" {
		return "", fmt.Errorf("discovery file %q is empty", path)
	}
	return raw, nil
}
