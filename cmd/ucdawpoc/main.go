// Command ucdawpoc is an unpublished Studio Pro UCNET transport probe.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

type dawClient interface {
	daw.Controller
	Close() error
}

var (
	scanDAWs   = daw.Scan
	connectDAW = func(
		ctx context.Context,
		endpoint daw.Endpoint,
	) (dawClient, error) {
		return daw.Connect(ctx, endpoint)
	}
)

func newRootCmd() *cobra.Command {
	var scanTimeout time.Duration
	root := &cobra.Command{
		Use:           "ucdawpoc",
		Short:         "Probe Studio Pro transport over UCNET",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().DurationVar(
		&scanTimeout, "scan-timeout", 3*time.Second, "DAW discovery window",
	)
	root.AddCommand(
		newDiscoverCmd(&scanTimeout),
		newTransportCmd(&scanTimeout),
	)
	return root
}

func newDiscoverCmd(scanTimeout *time.Duration) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "discover",
		Short: "Find Studio Pro DAWs",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			endpoints, err := scanDAWs(command.Context(), *scanTimeout)
			if err != nil {
				return err
			}
			if jsonOutput {
				encoder := json.NewEncoder(command.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(endpoints)
			}
			for _, endpoint := range endpoints {
				fmt.Fprintf(
					command.OutOrStdout(), "%s %s — %s (%s:%d)\n",
					endpoint.Product, endpoint.Version, endpoint.Name,
					endpoint.IP, endpoint.ControlPort,
				)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	return command
}

func newTransportCmd(scanTimeout *time.Duration) *cobra.Command {
	transportCommand := &cobra.Command{
		Use:   "transport",
		Short: "Control Studio Pro transport",
	}

	var duration time.Duration
	playCommand := &cobra.Command{
		Use:   "play",
		Short: "Play briefly, then stop",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					return daw.PulsePlayback(
						command.Context(), client, duration,
					)
				},
			)
			if err == nil {
				fmt.Fprintln(
					command.OutOrStdout(),
					"playback confirmed; Studio Pro stopped",
				)
			}
			return err
		},
	}
	playCommand.Flags().DurationVar(
		&duration, "duration", time.Second,
		"playback duration (maximum 1s)",
	)

	stopCommand := &cobra.Command{
		Use:   "stop",
		Short: "Stop playback",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			err := withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					return client.Trigger(command.Context(), daw.Stop)
				},
			)
			if err == nil {
				fmt.Fprintln(command.OutOrStdout(), "stop confirmed")
			}
			return err
		},
	}

	transportCommand.AddCommand(playCommand, stopCommand)
	return transportCommand
}

func withClient(
	ctx context.Context,
	scanTimeout time.Duration,
	fn func(dawClient) error,
) error {
	endpoints, err := scanDAWs(ctx, scanTimeout)
	if err != nil {
		return err
	}
	endpoint, err := daw.SelectStudioPro(endpoints)
	if err != nil {
		return err
	}
	client, err := connectDAW(ctx, endpoint)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	return fn(client)
}

func main() {
	ctx, cancel := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer cancel()
	command := newRootCmd()
	command.SetContext(ctx)
	if err := command.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
