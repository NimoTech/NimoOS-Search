package config

import (
	"os"
	"strconv"

	"gopkg.in/ini.v1"
)

type Config struct {
	BindHost             string
	RerankerEnabled      bool
	RerankerCandidates   int
	DefaultTopK          int
	MaxTopK              int
	EmbedCacheSize       int
	EmbedCacheTTLSec     int
	UserRootsCacheTTLSec int
	ParserTimeoutSec     int
	QdrantURL            string
	QdrantGRPCPort       int

	ParserDiscoveryPath  string
	NimoOSDiscoveryPath  string
	GatewayDiscoveryPath string
	MessageBusSocket     string

	RuntimePath string
	DataPath    string
	LogPath     string

	// fileindex
	FileIndexEnabled               bool
	FileIndexRoots                 []string
	FileIndexExclude               []string
	FileIndexDBPath                string
	FileIndexScanIntervalH         int
	FileIndexDegradedScanIntervalH int
	FileIndexWalkThrottleEvery     int
	FileIndexWalkThrottleSleepMs   int

	// photos proxy
	PhotosDiscoveryPath string

	// aggregate
	AggSemanticTopK    int
	AggFilenameTopK    int
	AggImageTopK       int
	AggNotesTopK       int
	AggMaxTotalResults int

	// runtime-mutable settings overlay
	SettingsPath string
}

func defaults() Config {
	return Config{
		BindHost:             "127.0.0.1",
		RerankerEnabled:      true,
		// 8, not 40: the reranker is CPU-bound on most NimoOS boxes (~1.3s per
		// candidate for real chunks), and 40 candidates blew past ParserTimeoutSec
		// so every rerank was abandoned - the query then reported
		// rerank_unavailable and, before the parser threadpool fix, dragged the
		// following path expansion down with it.
		RerankerCandidates:   8,
		DefaultTopK:          20,
		MaxTopK:              100,
		EmbedCacheSize:       1000,
		EmbedCacheTTLSec:     300,
		UserRootsCacheTTLSec: 60,
		ParserTimeoutSec:     10,
		QdrantURL:            "http://127.0.0.1:6333",
		QdrantGRPCPort:       6334,
		ParserDiscoveryPath:  "/var/run/nimoos/parser.url",
		NimoOSDiscoveryPath:  "/var/run/nimoos/nimoos.url",
		GatewayDiscoveryPath: "/var/run/nimoos/management.url",
		MessageBusSocket:     "/var/run/nimoos/message-bus.sock",
		RuntimePath:          "/var/run/nimoos",
		DataPath:             "/var/lib/nimoos/search",
		LogPath:              "/var/log/nimoos",
		FileIndexEnabled:     true,
		FileIndexRoots:       []string{"/DATA", "/mnt", "/media"},
		// NimoOS overlay-root system mounts that live under /mnt and /media.
		// They expose the whole OS root filesystem and system partitions; left in,
		// the boot scan indexes hundreds of thousands of system files and pegs a
		// CPU core for many minutes. Excluded by default; override via [fileindex] Exclude.
		FileIndexExclude:               []string{"/media/root-ro", "/media/root-rw", "/mnt/overlay", "/mnt/metadata"},
		FileIndexDBPath:                "/var/lib/nimoos/db/search.db",
		FileIndexScanIntervalH:         6,
		FileIndexDegradedScanIntervalH: 1,
		// Throttle the boot scan / reconcile WalkDir loop so indexing a huge tree
		// doesn't pin a CPU core. Every 1000 entries visited, sleep 50ms.
		FileIndexWalkThrottleEvery:   1000,
		FileIndexWalkThrottleSleepMs: 50,
		PhotosDiscoveryPath:          "/var/run/nimoos/photos.url",
		AggSemanticTopK:              5,
		AggFilenameTopK:              5,
		AggImageTopK:                 5,
		AggNotesTopK:                 5,
		AggMaxTotalResults:           15,
		SettingsPath:                 "/var/lib/nimoos/search-settings.json",
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
		if k, _ := s.GetKey("BindHost"); k != nil {
			c.BindHost = k.String()
		}
		if k, _ := s.GetKey("RerankerEnabled"); k != nil {
			c.RerankerEnabled, _ = k.Bool()
		}
		if k, _ := s.GetKey("RerankerCandidates"); k != nil {
			c.RerankerCandidates, _ = k.Int()
		}
		if k, _ := s.GetKey("DefaultTopK"); k != nil {
			c.DefaultTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("MaxTopK"); k != nil {
			c.MaxTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("EmbedCacheSize"); k != nil {
			c.EmbedCacheSize, _ = k.Int()
		}
		if k, _ := s.GetKey("EmbedCacheTtlSec"); k != nil {
			c.EmbedCacheTTLSec, _ = k.Int()
		}
		if k, _ := s.GetKey("UserRootsCacheTtlSec"); k != nil {
			c.UserRootsCacheTTLSec, _ = k.Int()
		}
		if k, _ := s.GetKey("ParserTimeoutSec"); k != nil {
			c.ParserTimeoutSec, _ = k.Int()
		}
		if k, _ := s.GetKey("QdrantUrl"); k != nil {
			c.QdrantURL = k.String()
		}
		if k, _ := s.GetKey("QdrantGrpcPort"); k != nil {
			c.QdrantGRPCPort, _ = k.Int()
		}
	}
	if s := f.Section("upstream"); s != nil {
		if k, _ := s.GetKey("ParserDiscoveryPath"); k != nil {
			c.ParserDiscoveryPath = k.String()
		}
		if k, _ := s.GetKey("NimoOSDiscoveryPath"); k != nil {
			c.NimoOSDiscoveryPath = k.String()
		}
		if k, _ := s.GetKey("GatewayDiscoveryPath"); k != nil {
			c.GatewayDiscoveryPath = k.String()
		}
		if k, _ := s.GetKey("MessageBusSocket"); k != nil {
			c.MessageBusSocket = k.String()
		}
	}
	if s := f.Section("common"); s != nil {
		if k, _ := s.GetKey("RuntimePath"); k != nil {
			c.RuntimePath = k.String()
		}
		if k, _ := s.GetKey("DataPath"); k != nil {
			c.DataPath = k.String()
		}
		if k, _ := s.GetKey("LogPath"); k != nil {
			c.LogPath = k.String()
		}
	}
	if s := f.Section("fileindex"); s != nil {
		if k, _ := s.GetKey("Enabled"); k != nil {
			c.FileIndexEnabled, _ = k.Bool()
		}
		if k, _ := s.GetKey("Roots"); k != nil {
			c.FileIndexRoots = k.Strings(",")
		}
		if k, _ := s.GetKey("Exclude"); k != nil {
			c.FileIndexExclude = k.Strings(",")
		}
		if k, _ := s.GetKey("DBPath"); k != nil {
			c.FileIndexDBPath = k.String()
		}
		if k, _ := s.GetKey("ScanIntervalH"); k != nil {
			c.FileIndexScanIntervalH, _ = k.Int()
		}
		if k, _ := s.GetKey("DegradedScanIntervalH"); k != nil {
			c.FileIndexDegradedScanIntervalH, _ = k.Int()
		}
		if k, _ := s.GetKey("WalkThrottleEvery"); k != nil {
			c.FileIndexWalkThrottleEvery, _ = k.Int()
		}
		if k, _ := s.GetKey("WalkThrottleSleepMs"); k != nil {
			c.FileIndexWalkThrottleSleepMs, _ = k.Int()
		}
	}
	if s := f.Section("photos"); s != nil {
		if k, _ := s.GetKey("DiscoveryPath"); k != nil {
			c.PhotosDiscoveryPath = k.String()
		}
	}
	if s := f.Section("aggregate"); s != nil {
		if k, _ := s.GetKey("SemanticTopK"); k != nil {
			c.AggSemanticTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("FilenameTopK"); k != nil {
			c.AggFilenameTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("ImageTopK"); k != nil {
			c.AggImageTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("NotesTopK"); k != nil {
			c.AggNotesTopK, _ = k.Int()
		}
		if k, _ := s.GetKey("MaxTotalResults"); k != nil {
			c.AggMaxTotalResults, _ = k.Int()
		}
	}
}

func applyEnv(c *Config) {
	if v := os.Getenv("SEARCH_BIND_HOST"); v != "" {
		c.BindHost = v
	}
	if v := os.Getenv("SEARCH_DEFAULT_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.DefaultTopK = n
		}
	}
	if v := os.Getenv("SEARCH_MAX_TOP_K"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.MaxTopK = n
		}
	}
	if v := os.Getenv("SEARCH_EMBED_CACHE_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.EmbedCacheSize = n
		}
	}
	if v := os.Getenv("SEARCH_QDRANT_URL"); v != "" {
		c.QdrantURL = v
	}
	if v := os.Getenv("SEARCH_QDRANT_GRPC_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.QdrantGRPCPort = n
		}
	}
	if v := os.Getenv("SEARCH_PARSER_DISCOVERY_PATH"); v != "" {
		c.ParserDiscoveryPath = v
	}
	if v := os.Getenv("SEARCH_NIMOOS_DISCOVERY_PATH"); v != "" {
		c.NimoOSDiscoveryPath = v
	}
	if v := os.Getenv("SEARCH_GATEWAY_DISCOVERY_PATH"); v != "" {
		c.GatewayDiscoveryPath = v
	}
	if v := os.Getenv("SEARCH_FILEINDEX_ENABLED"); v != "" {
		c.FileIndexEnabled = v == "1" || v == "true"
	}
	if v := os.Getenv("SEARCH_PHOTOS_DISCOVERY_PATH"); v != "" {
		c.PhotosDiscoveryPath = v
	}
}
