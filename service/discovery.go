package service

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

// BaseURLSource resolves a peer's base URL from its /var/run/nimoos *.url
// discovery file, caches it, and re-resolves on demand. NimoOS services
// write a NEW random port on every restart; a base URL frozen at startup
// strands the consumer until its own restart (bitten three times across
// the throttled-indexing project — this is that follow-up).
type BaseURLSource struct {
	mu       sync.Mutex
	urlFile  string
	fallback string
	cached   string
}

func NewBaseURLSource(urlFile, fallback string) *BaseURLSource {
	return &BaseURLSource{urlFile: urlFile, fallback: fallback}
}

func (s *BaseURLSource) read() string {
	b, err := os.ReadFile(s.urlFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Get returns the cached base URL, reading the discovery file on the first
// call only. Use Refresh to force a re-read (e.g. after a transport error).
func (s *BaseURLSource) Get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cached == "" {
		if u := s.read(); u != "" {
			s.cached = u
		}
	}
	if s.cached == "" {
		return s.fallback
	}
	return s.cached
}

// Refresh force re-reads the discovery file, updating the cache when a
// value is present, and returns the resulting base URL (falling back to the
// previously cached value, then to the configured fallback).
func (s *BaseURLSource) Refresh() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.read(); u != "" {
		s.cached = u
	}
	if s.cached == "" {
		return s.fallback
	}
	return s.cached
}

// doWithRediscover issues an HTTP request built by `build` against the
// peer's current base URL. On a transport-level error (err != nil from
// http.Client.Do — never an HTTP 4xx/5xx status, which callers must handle
// themselves) it re-resolves the peer's discovery file via src.Refresh()
// and retries exactly once. Shared by all peer clients (wiki/parser/
// notes/photos) so the retry policy lives in one place.
func doWithRediscover(hc *http.Client, src *BaseURLSource, build func(base string) (*http.Request, error)) (*http.Response, error) {
	req, err := build(src.Get())
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err == nil {
		return resp, nil
	}
	req2, berr := build(src.Refresh())
	if berr != nil {
		return nil, err
	}
	return hc.Do(req2)
}
