package dawserve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/daw"
	"github.com/steveclarke/ucmix/internal/dawschema"
)

func startServer(t *testing.T) (Client, *Server) {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "dawschema", "testdata", "mini.xml"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	schema, err := dawschema.Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	// Unix socket paths are length-limited; t.TempDir() can be too long on macOS.
	dir, err := os.MkdirTemp("/tmp", "ucdaw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s.sock")
	srv := &Server{
		Schema: schema,
		Connect: func(context.Context) (*daw.Session, error) {
			return nil, daw.NotFound
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, socket) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	client := Client{Socket: socket}
	for i := 0; i < 100 && !client.Available(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	return client, srv
}

func TestClientRoundTripsParamsWithoutStudioPro(t *testing.T) {
	client, _ := startServer(t)
	res, err := client.Do(context.Background(), daw.Request{Op: "params", Filter: "tempo"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Message, "1 parameter match") {
		t.Errorf("message = %q", res.Message)
	}
}

func TestClientCarriesProblemAndHint(t *testing.T) {
	client, _ := startServer(t)
	_, err := client.Do(context.Background(), daw.Request{Op: "song"})
	var p daw.Problem
	if !errors.As(err, &p) || p.Message != daw.NotFound.Message || p.Hint == "" {
		t.Errorf("err = %#v", err)
	}
}

func TestStatusWhenDisconnected(t *testing.T) {
	client, _ := startServer(t)
	res, err := client.Do(context.Background(), daw.Request{Op: "status"})
	if err != nil || !strings.Contains(res.Message, "not connected") {
		t.Errorf("status = %q, %v", res.Message, err)
	}
}

func TestSecondServeRefuses(t *testing.T) {
	client, srv := startServer(t)
	err := srv.Serve(context.Background(), client.Socket)
	var p daw.Problem
	if !errors.As(err, &p) || !strings.Contains(p.Message, "already running") {
		t.Errorf("err = %v", err)
	}
}
