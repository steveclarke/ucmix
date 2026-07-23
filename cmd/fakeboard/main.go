// Command fakeboard runs the in-process fake UCNET mixer as a standalone binary
// for BATS/E2E tests. It is TEST-ONLY and excluded from release builds (the
// GoReleaser config builds only ./cmd/ucmix).
//
// Usage:
//
//	fakeboard [--seed tree.json] [--port 0]
//
// --seed points at a JSON object mapping flat "/"-delimited paths to values
// (numbers, strings, or bools). Bools are coerced to 1.0/0.0. If omitted, a
// small built-in seed is used. The bound address (host:port) is printed on
// stdout so a harness can read it. The board runs until the process is killed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/steveclarke/ucmix/internal/fakeboard"
)

func main() {
	seedPath := flag.String("seed", "", "path to a JSON flat path→value seed map (built-in seed if empty)")
	flag.Int("port", 0, "ignored; the board always binds an ephemeral 127.0.0.1 port")
	flag.Parse()

	seed := builtinSeed()
	if *seedPath != "" {
		loaded, err := loadSeed(*seedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakeboard: %v\n", err)
			os.Exit(1)
		}
		seed = loaded
	}

	b := fakeboard.New(seed)
	addr, err := b.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeboard: start: %v\n", err)
		os.Exit(1)
	}
	// Print the bound address so a test harness can connect.
	fmt.Println(addr)

	// Block forever; the harness kills the process when done.
	select {}
}

// loadSeed reads a JSON object of flat path→value pairs from path.
func loadSeed(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading seed %q: %w", path, err)
	}
	var seed map[string]any
	if err := json.Unmarshal(data, &seed); err != nil {
		return nil, fmt.Errorf("parsing seed %q: %w", path, err)
	}
	return seed, nil
}

// builtinSeed is a small generic default tree used when --seed is omitted.
func builtinSeed() map[string]any {
	return map[string]any{
		"global/mixer_name": "Fakeboard",
		"line/ch1/name":     "Ch 1",
		"line/ch1/mute":     0.0,
		"line/ch1/volume":   0.75,
		"main/ch1/volume":   0.75,
	}
}
