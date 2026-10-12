package daw

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestConnectUsesCachedEndpointThenDiscovery(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "endpoint.json")
	good := Endpoint{DeviceClass: "DAW", Identity: "x.studioapp8", IP: "10.0.0.2", ControlPort: 5000, ServerID: 7}
	stale := good
	stale.ControlPort = 4000
	writeEndpoint(cache, stale)

	origFind, origDial := findStudioPro, dialSession
	t.Cleanup(func() { findStudioPro, dialSession = origFind, origDial })
	var dialed []int
	dialSession = func(_ context.Context, e Endpoint) (*Session, error) {
		dialed = append(dialed, e.ControlPort)
		if e.ControlPort != good.ControlPort {
			return nil, errors.New("refused")
		}
		return &Session{}, nil
	}
	findStudioPro = func(context.Context, time.Duration) (Endpoint, error) { return good, nil }

	if _, err := Connect(context.Background(), cache); err != nil {
		t.Fatal(err)
	}
	if len(dialed) != 2 || dialed[0] != 4000 || dialed[1] != 5000 {
		t.Errorf("dialed = %v, want stale cache then discovered", dialed)
	}
	if e, ok := readEndpoint(cache); !ok || e != good {
		t.Errorf("cache = %+v, %v", e, ok)
	}
}

func TestConnectExplainsMissingStudioPro(t *testing.T) {
	origFind := findStudioPro
	t.Cleanup(func() { findStudioPro = origFind })
	findStudioPro = func(context.Context, time.Duration) (Endpoint, error) { return Endpoint{}, ErrNoStudioPro }
	_, err := Connect(context.Background(), "")
	var p Problem
	if !errors.As(err, &p) || p.Hint == "" {
		t.Errorf("err = %v", err)
	}
}
