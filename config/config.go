package config

import (
	"os"
	"strconv"

	"gopkg.in/ini.v1"
)

type Config struct {
	BindHost              string
	RerankerEnabled       bool
	RerankerCandidates    int
	DefaultTopK           int
	MaxTopK               int
	EmbedCacheSize        int
	EmbedCacheTTLSec      int
	UserRootsCacheTTLSec  int
	ParserTimeoutSec      int
	WikiTimeoutSec        int
	QdrantURL             string
	QdrantGRPCPort        int

	ParserDiscoveryPath  string
	WikiDiscoveryPath    string
	GatewayDiscoveryPath string
	MessageBusSocket     string

	RuntimePath string
	DataPath    string
	LogPath     string
}

func defaults() Config {
	return Config{
		BindHost:             "127.0.0.1",
		RerankerEnabled:      true,
		RerankerCandidates:   40,
		DefaultTopK:          20,
		MaxTopK:              100,
		EmbedCacheSize:       1000,
		EmbedCacheTTLSec:     300,
		UserRootsCacheTTLSec: 60,
		ParserTimeoutSec:     10,
		WikiTimeoutSec:       5,
		QdrantURL:            "http://127.0.0.1:6333",
		QdrantGRPCPort:       6334,
		ParserDiscoveryPath:  "/var/run/nimoos/parser.url",
		WikiDiscoveryPath:    "/var/run/nimoos/wiki.url",
		GatewayDiscoveryPath: "/var/run/nimoos/gateway.url",
		MessageBusSocket:     "/var/run/nimoos/message-bus.sock",
		RuntimePath:          "/var/run/nimoos",
		DataPath:             "/var/lib/nimoos/search",
		LogPath:              "/var/log/nimoos",
	}
}

// Load reads an INI file (missing is OK, defaults are used) then applies env
// overrides keyed as SEARCH_<UPPER_SNAKE_CASE> for each Config field.
func Load(path string) (Config, error) {
	c := defaults()
	if _, err := os.Stat(path); err == nil {
		f, err := ini.Load(path)
		if err != nil {
			return c, err
		}
		applyINI(f, &c)
	}
	applyEnv(&c)
	return c, nil
}

func applyINI(f *ini.File, c *Config) {
	if s := f.Section("search"); s != nil {
		if k, _ := s.GetKey("BindHost"); k != nil { c.BindHost = k.String() }
		if k, _ := s.GetKey("RerankerEnabled"); k != nil { c.RerankerEnabled, _ = k.Bool() }
		if k, _ := s.GetKey("RerankerCandidates"); k != nil { c.RerankerCandidates, _ = k.Int() }
		if k, _ := s.GetKey("DefaultTopK"); k != nil { c.DefaultTopK, _ = k.Int() }
		if k, _ := s.GetKey("MaxTopK"); k != nil { c.MaxTopK, _ = k.Int() }
		if k, _ := s.GetKey("EmbedCacheSize"); k != nil { c.EmbedCacheSize, _ = k.Int() }
		if k, _ := s.GetKey("EmbedCacheTtlSec"); k != nil { c.EmbedCacheTTLSec, _ = k.Int() }
		if k, _ := s.GetKey("UserRootsCacheTtlSec"); k != nil { c.UserRootsCacheTTLSec, _ = k.Int() }
		if k, _ := s.GetKey("ParserTimeoutSec"); k != nil { c.ParserTimeoutSec, _ = k.Int() }
		if k, _ := s.GetKey("WikiTimeoutSec"); k != nil { c.WikiTimeoutSec, _ = k.Int() }
		if k, _ := s.GetKey("QdrantUrl"); k != nil { c.QdrantURL = k.String() }
		if k, _ := s.GetKey("QdrantGrpcPort"); k != nil { c.QdrantGRPCPort, _ = k.Int() }
	}
	if s := f.Section("upstream"); s != nil {
		if k, _ := s.GetKey("ParserDiscoveryPath"); k != nil { c.ParserDiscoveryPath = k.String() }
		if k, _ := s.GetKey("WikiDiscoveryPath"); k != nil { c.WikiDiscoveryPath = k.String() }
		if k, _ := s.GetKey("GatewayDiscoveryPath"); k != nil { c.GatewayDiscoveryPath = k.String() }
		if k, _ := s.GetKey("MessageBusSocket"); k != nil { c.MessageBusSocket = k.String() }
	}
	if s := f.Section("common"); s != nil {
		if k, _ := s.GetKey("RuntimePath"); k != nil { c.RuntimePath = k.String() }
		if k, _ := s.GetKey("DataPath"); k != nil { c.DataPath = k.String() }
		if k, _ := s.GetKey("LogPath"); k != nil { c.LogPath = k.String() }
	}
}

func applyEnv(c *Config) {
	if v := os.Getenv("SEARCH_BIND_HOST"); v != "" { c.BindHost = v }
	if v := os.Getenv("SEARCH_DEFAULT_TOP_K"); v != "" { if n, err := strconv.Atoi(v); err == nil { c.DefaultTopK = n } }
	if v := os.Getenv("SEARCH_MAX_TOP_K"); v != "" { if n, err := strconv.Atoi(v); err == nil { c.MaxTopK = n } }
	if v := os.Getenv("SEARCH_EMBED_CACHE_SIZE"); v != "" { if n, err := strconv.Atoi(v); err == nil { c.EmbedCacheSize = n } }
	if v := os.Getenv("SEARCH_QDRANT_URL"); v != "" { c.QdrantURL = v }
	if v := os.Getenv("SEARCH_QDRANT_GRPC_PORT"); v != "" { if n, err := strconv.Atoi(v); err == nil { c.QdrantGRPCPort = n } }
	if v := os.Getenv("SEARCH_PARSER_DISCOVERY_PATH"); v != "" { c.ParserDiscoveryPath = v }
	if v := os.Getenv("SEARCH_WIKI_DISCOVERY_PATH"); v != "" { c.WikiDiscoveryPath = v }
	if v := os.Getenv("SEARCH_GATEWAY_DISCOVERY_PATH"); v != "" { c.GatewayDiscoveryPath = v }
}
