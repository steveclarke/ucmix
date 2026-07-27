package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steveclarke/ucmix/internal/daw"
)

type fakeDAWClient struct {
	actions []daw.Action
	closed  bool
}

func (f *fakeDAWClient) Trigger(
	_ context.Context,
	action daw.Action,
) error {
	f.actions = append(f.actions, action)
	return nil
}

func (f *fakeDAWClient) Close() error {
	f.closed = true
	return nil
}

func useCommandFakes(
	t *testing.T,
	endpoints []daw.Endpoint,
) *fakeDAWClient {
	t.Helper()
	client := &fakeDAWClient{}
	oldScan, oldConnect := scanDAWs, connectDAW
	scanDAWs = func(
		context.Context,
		time.Duration,
	) ([]daw.Endpoint, error) {
		return endpoints, nil
	}
	connectDAW = func(
		context.Context,
		daw.Endpoint,
	) (dawClient, error) {
		return client, nil
	}
	t.Cleanup(func() {
		scanDAWs, connectDAW = oldScan, oldConnect
	})
	return client
}

func executeCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	command := newRootCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(args)
	err := command.ExecuteContext(context.Background())
	return output.String(), err
}

func testEndpoint(identity string) daw.Endpoint {
	return daw.Endpoint{
		Product:     "Studio Pro",
		Version:     "8.1.1.113103 macOS Arm64",
		DeviceClass: "DAW",
		Identity:    identity,
		Name:        "Test Mac mini",
		IP:          "192.0.2.20",
		ControlPort: 49601,
		ServerID:    0x6b,
	}
}

func TestDiscoverPrintsDAWs(t *testing.T) {
	useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(t, "discover")
	if err != nil {
		t.Fatal(err)
	}
	want := "Studio Pro 8.1.1.113103 macOS Arm64 — " +
		"Test Mac mini (192.0.2.20:49601)\n"
	if output != want {
		t.Fatalf("output = %q, want %q", output, want)
	}
}

func TestTransportStopDiscoversConnectsAndStops(t *testing.T) {
	client := useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(t, "transport", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(client.actions, []daw.Action{daw.Stop}) {
		t.Fatalf("actions = %v", client.actions)
	}
	if output != "stop confirmed\n" || !client.closed {
		t.Fatalf("output = %q, closed = %v", output, client.closed)
	}
}

func TestTransportPlayUsesGuardedPulse(t *testing.T) {
	client := useCommandFakes(t, []daw.Endpoint{
		testEndpoint("sanitized.studioapp8"),
	})
	output, err := executeCommand(
		t, "transport", "play", "--duration", "0s",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []daw.Action{daw.Stop, daw.Play, daw.Stop}
	if !reflect.DeepEqual(client.actions, want) {
		t.Fatalf("actions = %v, want %v", client.actions, want)
	}
	if output != "playback confirmed; Studio Pro stopped\n" {
		t.Fatalf("output = %q", output)
	}
}

func TestTransportRejectsZeroMatches(t *testing.T) {
	useCommandFakes(t, nil)
	_, err := executeCommand(t, "transport", "stop")
	if !errors.Is(err, daw.ErrNoStudioPro) {
		t.Fatalf("error = %v", err)
	}
}

func TestTransportRejectsMultipleMatches(t *testing.T) {
	useCommandFakes(t, []daw.Endpoint{
		testEndpoint("one.studioapp8"),
		testEndpoint("two.studioapp8"),
	})
	_, err := executeCommand(t, "transport", "stop")
	if !errors.Is(err, daw.ErrMultipleStudioPro) {
		t.Fatalf("error = %v", err)
	}
}

func TestHelpOmitsGeneratedCompletionCommand(t *testing.T) {
	output, err := executeCommand(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output, "completion") {
		t.Fatalf("help exposed completion command:\n%s", output)
	}
}
