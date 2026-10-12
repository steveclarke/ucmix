package daw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveclarke/ucmix/internal/dawschema"
)

// Test seams for connecting.
var (
	findStudioPro = FindStudioPro
	dialSession   = Dial
)

// discoveryWindow is how long to listen for Studio Pro's announcement.
const discoveryWindow = 3 * time.Second

// NotFound is the problem a musician sees when Studio Pro is not reachable.
var NotFound = Problem{
	Message: "Studio Pro is not answering on this network.",
	Hint:    "open Studio Pro with a song and make sure its Remote service is on; `ucdaw discover` should list it",
}

// Connect finds Studio Pro (the cached endpoint first, then discovery) and
// opens a subscribed session. cacheFile may be empty to skip the cache. A
// non-empty host pins Studio Pro to that IP for both the cache and discovery.
func Connect(ctx context.Context, cacheFile, host string) (*Session, error) {
	if cached, ok := readEndpoint(cacheFile); ok && (host == "" || cached.IP == host) {
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		s, err := dialSession(dialCtx, cached)
		cancel()
		if err == nil {
			return s, nil
		}
	}
	endpoint, err := findStudioPro(ctx, discoveryWindow, host)
	if errors.Is(err, ErrNoStudioPro) {
		return nil, NotFound
	}
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	s, err := dialSession(dialCtx, endpoint)
	cancel()
	if err != nil {
		return nil, Problem{
			Message: fmt.Sprintf("Found Studio Pro on %s but could not connect: %v", endpoint.Name, err),
			Hint:    "Studio Pro may be busy with a dialog; close it and try again",
		}
	}
	writeEndpoint(cacheFile, endpoint)
	return s, nil
}

// DefaultCacheFile is where the last Studio Pro endpoint is remembered.
func DefaultCacheFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "ucdaw", "endpoint.json")
}

func readEndpoint(path string) (Endpoint, bool) {
	if path == "" {
		return Endpoint{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Endpoint{}, false
	}
	var e Endpoint
	if json.Unmarshal(data, &e) != nil || !isStudioPro(e) || e.ControlPort == 0 {
		return Endpoint{}, false
	}
	return e, true
}

func writeEndpoint(path string, e Endpoint) {
	if path == "" {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

// Direct runs each request on a fresh session: connect, act, disconnect.
type Direct struct {
	Schema    *dawschema.Catalog
	CacheFile string
	Host      string
}

// Do implements Engine.
func (d Direct) Do(ctx context.Context, req Request) (Result, error) {
	switch req.Op {
	case "params":
		return Params(d.Schema, req.Filter), nil
	case "plugins":
		return Plugins(req.Filter)
	}
	s, err := Connect(ctx, d.CacheFile, d.Host)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = s.Close() }()
	return NewStudio(s, d.Schema).Do(ctx, req)
}
