// Command ucdawpoc is an unpublished Studio Pro UCNET transport probe.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
	"github.com/steveclarke/ucmix/internal/proto"
)

type dawClient interface {
	daw.Controller
	Close() error
}

type parameterClient interface {
	SetParameter(context.Context, string, float32) error
}

type labClient interface {
	Frames() <-chan proto.Frame
	SendParameter(context.Context, string, float32) error
	Gesture(context.Context, string, float32) error
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
		newParameterCmd(&scanTimeout),
		newLabCmd(&scanTimeout),
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

func newLabCmd(scanTimeout *time.Duration) *cobra.Command {
	return &cobra.Command{
		Use:   "lab",
		Short: "Observe and replay Studio Pro controls on one connection",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					lab, ok := client.(labClient)
					if !ok {
						return fmt.Errorf("DAW client does not support lab mode")
					}
					return runLab(command, lab)
				},
			)
		},
	}
}

func runLab(command *cobra.Command, client labClient) error {
	lines := make(chan string)
	scanErr := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(command.InOrStdin())
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		scanErr <- scanner.Err()
	}()

	fmt.Fprintln(
		command.OutOrStdout(),
		"connected; commands: pv <path> <value>, gesture <path> <value>, play, stop, quit",
	)
	for {
		select {
		case frame, ok := <-client.Frames():
			if !ok {
				return daw.ErrConnectionClosed
			}
			printLabFrame(command, frame)
		case line := <-lines:
			done, err := runLabCommand(command.Context(), client, line)
			if err != nil {
				fmt.Fprintf(command.ErrOrStderr(), "error: %v\n", err)
			}
			if done {
				return nil
			}
		case err := <-scanErr:
			return err
		case <-command.Context().Done():
			return command.Context().Err()
		}
	}
}

func printLabFrame(command *cobra.Command, frame proto.Frame) {
	switch frame.Code {
	case proto.CodePV:
		key, value, err := proto.UnmarshalPV(frame.Payload)
		if err == nil {
			fmt.Fprintf(command.OutOrStdout(), "PV %s=%g\n", key, value)
		}
	case proto.CodePE:
		key, state, err := proto.UnmarshalPE(frame.Payload)
		if err == nil {
			fmt.Fprintf(command.OutOrStdout(), "PE %s state=%d\n", key, state)
		}
	}
}

func runLabCommand(
	ctx context.Context,
	client labClient,
	line string,
) (bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false, nil
	}
	switch fields[0] {
	case "quit", "exit":
		return true, nil
	case "play":
		return false, client.Gesture(ctx, "transport/start", 1)
	case "stop":
		return false, client.Gesture(ctx, "transport/stop", 1)
	case "pv", "gesture":
		if len(fields) != 3 {
			return false, fmt.Errorf("%s requires <path> <value>", fields[0])
		}
		parsed, err := strconv.ParseFloat(fields[2], 32)
		if err != nil {
			return false, fmt.Errorf("invalid value %q: %w", fields[2], err)
		}
		value := float32(parsed)
		if fields[0] == "pv" {
			return false, client.SendParameter(ctx, fields[1], value)
		}
		return false, client.Gesture(ctx, fields[1], value)
	default:
		return false, fmt.Errorf("unknown lab command %q", fields[0])
	}
}

func newParameterCmd(scanTimeout *time.Duration) *cobra.Command {
	parameterCommand := &cobra.Command{
		Use:   "parameter",
		Short: "Control a Studio Pro parameter",
	}
	var pulse time.Duration
	setCommand := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Set and confirm one float parameter",
		Args:  cobra.ExactArgs(2),
		RunE: func(command *cobra.Command, args []string) error {
			parsed, err := strconv.ParseFloat(args[1], 32)
			if err != nil {
				return fmt.Errorf("invalid value %q: %w", args[1], err)
			}
			err = withClient(
				command.Context(), *scanTimeout,
				func(client dawClient) error {
					setter, ok := client.(parameterClient)
					if !ok {
						return fmt.Errorf("DAW client cannot set parameters")
					}
					if err := setter.SetParameter(
						command.Context(), args[0], float32(parsed),
					); err != nil {
						return err
					}
					if pulse > 0 {
						return daw.PulsePlayback(
							command.Context(), client, pulse,
						)
					}
					return nil
				},
			)
			if err == nil {
				fmt.Fprintf(
					command.OutOrStdout(), "%s=%g confirmed\n",
					args[0], float32(parsed),
				)
			}
			return err
		},
	}
	setCommand.Flags().DurationVar(
		&pulse, "pulse", 0,
		"play briefly on the same connection after setting the parameter",
	)
	parameterCommand.AddCommand(setCommand)
	return parameterCommand
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
