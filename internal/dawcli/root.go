// Package dawcli is the ucdaw command line: Studio Pro over UCNET, the way the
// Studio Pro Remote app drives it.
package dawcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
	"github.com/steveclarke/ucmix/internal/dawschema"
	"github.com/steveclarke/ucmix/internal/dawserve"
)

type options struct {
	json       bool
	direct     bool
	schemaPath string
	socket     string
	host       string
}

// Test seam: tests swap in an engine that never touches the network.
var newEngine = func(o *options) (daw.Engine, error) {
	client := dawserve.Client{Socket: o.socketPath()}
	if !o.direct && client.Available() {
		return client, nil
	}
	schema, err := loadSchema(o.schemaPath)
	if err != nil {
		return nil, err
	}
	return daw.Direct{Schema: schema, CacheFile: daw.DefaultCacheFile(), Host: o.host}, nil
}

// socketPath is --socket, or the private per-user default ("" if that
// directory cannot be made safe, which disables serve).
func (o *options) socketPath() string {
	if o.socket != "" {
		return o.socket
	}
	path, err := dawserve.DefaultSocket()
	if err != nil {
		return ""
	}
	return path
}

func loadSchema(path string) (*dawschema.Catalog, error) {
	if path == "" {
		path = os.Getenv("UCDAW_SCHEMA")
	}
	schema, err := dawschema.Load(path)
	if err != nil {
		return nil, daw.Problem{
			Message: "Could not read Studio Pro's remote model (dawremote.xml).",
			Hint:    "is Studio Pro 8 installed in /Applications? Otherwise point --schema or UCDAW_SCHEMA at the file",
		}
	}
	return schema, nil
}

func newRootCmd() *cobra.Command {
	o := &options{}
	root := &cobra.Command{
		Use:   "ucdaw",
		Short: "Drive Fender Studio Pro over UCNET, like the Studio Pro Remote app",
		Long: "ucdaw drives Fender Studio Pro 8 over UCNET, the protocol the Studio Pro Remote\n" +
			"app uses. Every change is read back from Studio Pro before ucdaw reports it.\n" +
			"Run `ucdaw serve` in the background for instant answers.\n\n" +
			"Unofficial; not affiliated with or endorsed by Fender or PreSonus.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	flags := root.PersistentFlags()
	flags.BoolVar(&o.json, "json", false, "print JSON")
	flags.BoolVar(&o.direct, "direct", false, "connect to Studio Pro directly even when serve is running")
	flags.StringVar(&o.schemaPath, "schema", "", "path to dawremote.xml (default: inside Studio Pro 8.app)")
	flags.StringVar(&o.socket, "socket", "", "serve socket path (default: a private per-user directory)")
	flags.StringVar(&o.host, "host", os.Getenv("UCDAW_HOST"), "only trust Studio Pro at this IP (or set UCDAW_HOST)")
	root.AddCommand(
		newDiscoverCmd(o),
		newTransportCmd(o),
		newClickCmd(o),
		newTempoCmd(o),
		newParamCmd(o),
		newParamsCmd(o),
		newStateCmd(o),
		newCommandCmd(o),
		newTrackCmd(o),
		newInsertCmd(o),
		newSectionCmd(o),
		newAudioCmd(o),
		newPluginsCmd(o),
		newServeCmd(o),
		newMCPCmd(o),
	)
	return root
}

// run sends one request and prints the answer.
func run(cmd *cobra.Command, o *options, req daw.Request) error {
	engine, err := newEngine(o)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), daw.MaxPlay+30*time.Second)
	defer cancel()
	res, err := engine.Do(ctx, req)
	if err != nil {
		return err
	}
	return printResult(cmd.OutOrStdout(), o, res)
}

func printResult(w io.Writer, o *options, res daw.Result) error {
	if o.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	if _, err := fmt.Fprintln(w, safe(res.Message)); err != nil {
		return err
	}
	return printRows(w, res.Data)
}

// printRows prints list results as aligned columns. Data that crossed the
// serve socket arrives as generic JSON, so both typed and decoded forms are
// handled by re-encoding into rows.
func printRows(w io.Writer, data any) error {
	raw, err := json.Marshal(data)
	if err != nil || len(raw) == 0 || raw[0] != '[' {
		return nil
	}
	var rows []map[string]any
	if json.Unmarshal(raw, &rows) != nil || len(rows) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, row := range rows {
		switch {
		case row["classId"] != nil:
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", cell(row["name"]), cell(row["vendor"]), cell(row["format"]))
		case row["category"] != nil:
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", cell(row["category"]), cell(row["name"]))
		case row["pattern"] != nil:
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\n", cell(row["pattern"]), cell(row["type"]), cell(describeParam(row)))
		case row["value"] != nil:
			_, _ = fmt.Fprintf(tw, "  %s\t%s\n", cell(row["path"]), cell(row["value"]))
		}
	}
	return tw.Flush()
}

// cell renders one table cell from Studio Pro data: no control characters and
// no tabs (a tab would break the columns).
func cell(v any) string {
	return strings.ReplaceAll(safe(fmt.Sprint(v)), "\t", " ")
}

func describeParam(row map[string]any) string {
	var parts []string
	switch row["type"] {
	case "float", "int":
		parts = append(parts, fmt.Sprintf("%v..%v", row["min"], row["max"]))
	case "list":
		if opts, ok := row["options"].([]any); ok {
			var s []string
			for _, o := range opts {
				s = append(s, fmt.Sprint(o))
			}
			parts = append(parts, strings.Join(s, "|"))
		}
	}
	if u, ok := row["units"].(string); ok && u != "" {
		parts = append(parts, u)
	}
	if flags, ok := row["flags"].([]any); ok {
		for _, f := range flags {
			parts = append(parts, fmt.Sprint(f))
		}
	}
	return strings.Join(parts, " ")
}

func newDiscoverCmd(o *options) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Find Studio Pro on the network",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			found, err := daw.Scan(cmd.Context(), timeout)
			if err != nil {
				return err
			}
			if o.json {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(found)
			}
			if len(found) == 0 {
				return daw.NotFound
			}
			for _, e := range found {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), discoverLine(e))
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Second, "how long to listen")
	return cmd
}

// discoverLine renders one announcement; every field came off the LAN.
func discoverLine(e daw.Endpoint) string {
	version := e.Version
	if fields := strings.Fields(version); len(fields) > 0 {
		version = fields[0]
	}
	return safe(fmt.Sprintf("%s %s on %s (%s:%d)", e.Product, version, e.Name, e.IP, e.ControlPort))
}

func newTransportCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transport",
		Short: "Play, stop, return to zero, loop",
	}
	var seconds time.Duration
	var bars float64
	play := &cobra.Command{
		Use:   "play",
		Short: "Start playback; with --for or --bars, stop again afterwards",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, o, daw.Request{Op: "transport", Action: "play", Seconds: seconds.Seconds(), Bars: bars})
		},
	}
	play.Flags().DurationVar(&seconds, "for", 0, "play this long, then stop (max 60s)")
	play.Flags().Float64Var(&bars, "bars", 0, "play this many bars at the song's tempo, then stop")
	simple := func(use, short, action string) *cobra.Command {
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return run(cmd, o, daw.Request{Op: "transport", Action: action})
			},
		}
	}
	loop := &cobra.Command{
		Use:       "loop [on|off]",
		Short:     "Turn the loop on or off (toggle with no argument)",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off"},
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "transport", Action: "loop"}
			if len(args) == 1 {
				req.Value = args[0]
			}
			return run(cmd, o, req)
		},
	}
	cmd.AddCommand(play, simple("stop", "Stop playback", "stop"),
		simple("rtz", "Return to the start of the song", "rtz"), loop)
	return cmd
}

func newClickCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:       "click [on|off|toggle]",
		Short:     "Turn the metronome click on or off (no argument: show it)",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"on", "off", "toggle"},
		RunE: func(cmd *cobra.Command, args []string) error {
			action := "status"
			if len(args) == 1 {
				action = args[0]
			}
			return run(cmd, o, daw.Request{Op: "click", Action: action})
		},
	}
}

func newTempoCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "tempo [bpm]",
		Short: "Show or set the song tempo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "tempo"}
			if len(args) == 1 {
				bpm, err := strconv.ParseFloat(args[0], 64)
				if err != nil || bpm <= 0 {
					return daw.Problem{Message: fmt.Sprintf("tempo needs a number of bpm, not %q", args[0])}
				}
				req.BPM = bpm
			}
			return run(cmd, o, req)
		},
	}
}

func newParamCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "param",
		Short: "Read or write any Studio Pro parameter by path",
	}
	get := &cobra.Command{
		Use:   "get <path>",
		Short: "Read one parameter (e.g. transport/tempo)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "get", Path: args[0]})
		},
	}
	set := &cobra.Command{
		Use:   "set <path> <value>",
		Short: "Write one parameter in its own units and confirm it (e.g. mixer/channel/ch1/mute on)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "set", Path: args[0], Value: strings.Join(args[1:], " ")})
		},
	}
	cmd.AddCommand(get, set)
	return cmd
}

func newParamsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "params [filter]",
		Short: "List the parameters Studio Pro exposes (from its dawremote.xml)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			schema, err := loadSchema(o.schemaPath)
			if err != nil {
				return err
			}
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			return printResult(cmd.OutOrStdout(), o, daw.Params(schema, filter))
		},
	}
}

func newStateCmd(o *options) *cobra.Command {
	var values bool
	cmd := &cobra.Command{
		Use:   "state [path-prefix]",
		Short: "Show the live song: tempo, click, tracks and plug-ins",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if values || len(args) == 1 {
				prefix := ""
				if len(args) == 1 {
					prefix = args[0]
				}
				return run(cmd, o, daw.Request{Op: "dump", Filter: prefix})
			}
			return run(cmd, o, daw.Request{Op: "song"})
		},
	}
	cmd.Flags().BoolVar(&values, "values", false, "list every live value instead of the summary")
	cmd.AddCommand(newRecordCmd(o))
	return cmd
}

func newRecordCmd(o *options) *cobra.Command {
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "record <file>",
		Short: "Record everything Studio Pro sends after subscribing (raw frames, for protocol work)",
		Long: "Records raw UCNET frames to <file>. The capture holds license and song data:\n" +
			"keep it out of public repositories.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := createCapture(args[0])
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			endpoint, err := daw.FindStudioPro(cmd.Context(), 3*time.Second, o.host)
			if errors.Is(err, daw.ErrNoStudioPro) {
				return daw.NotFound
			}
			if err != nil {
				return err
			}
			rec := daw.NewRecorder(f)
			s, err := daw.DialTap(cmd.Context(), endpoint, rec.Tap)
			if err != nil {
				return err
			}
			select {
			case <-time.After(duration):
			case <-cmd.Context().Done():
			case <-s.Done():
			}
			_ = s.Close()
			if err := rec.Err(); err != nil {
				return err
			}
			return printResult(cmd.OutOrStdout(), o, daw.Result{
				Message: fmt.Sprintf("Recorded %d frames to %s.", rec.Frames, args[0]),
				Data:    map[string]any{"frames": rec.Frames, "file": args[0]},
			})
		},
	}
	cmd.Flags().DurationVar(&duration, "for", 5*time.Second, "how long to record after subscribing")
	return cmd
}

// createCapture opens a capture file owner-only: captures hold license and
// song data. An existing file is tightened to 0600 as well.
func createCapture(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func newCommandCmd(o *options) *cobra.Command {
	var cmdArgs []string
	cmd := &cobra.Command{
		Use:   "command",
		Short: "List and run Studio Pro's own commands (any menu or key command)",
	}
	list := &cobra.Command{
		Use:   "list [filter]",
		Short: "List the commands Studio Pro offers, by category",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "commands"}
			if len(args) == 1 {
				req.Filter = args[0]
			}
			return run(cmd, o, req)
		},
	}
	runCmd := &cobra.Command{
		Use:   "run <category> <name> | run \"<category> > <name>\" | run <unique name>",
		Short: "Run a Studio Pro command and report what changed",
		Example: "  ucdaw command run Track \"Add Audio Track (mono)\"\n" +
			"  ucdaw command run \"Edit > Undo\"",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "run", Category: args[0]}
			if len(args) == 2 {
				req.Name = args[1]
			}
			parsed, err := commandArgs(cmdArgs)
			if err != nil {
				return err
			}
			req.Args = parsed
			return run(cmd, o, req)
		},
	}
	runCmd.Flags().StringArrayVar(&cmdArgs, "arg", nil, "command argument as name=value (repeatable), as a Studio Pro macro carries it")
	cmd.AddCommand(list, runCmd)
	return cmd
}

func newTrackCmd(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "track",
		Short: "Add tracks",
	}
	var name string
	add := &cobra.Command{
		Use:       "add [mono|stereo]",
		Short:     "Add an audio track (mono unless you say stereo), optionally named",
		Example:   "  ucdaw track add mono --name Guitar",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"mono", "stereo"},
		RunE: func(cmd *cobra.Command, args []string) error {
			req := daw.Request{Op: "add_track", Name: name}
			if len(args) == 1 {
				req.Action = args[0]
			}
			return run(cmd, o, req)
		},
	}
	add.Flags().StringVar(&name, "name", "", "name for the new track")
	cmd.AddCommand(add)
	return cmd
}

func newInsertCmd(o *options) *cobra.Command {
	var preset string
	cmd := &cobra.Command{
		Use:   "insert <channel> <plug-in>",
		Short: "Put a plug-in on a channel's inserts, optionally with one of its presets",
		Long: "Adds the plug-in after the channel's existing inserts. <channel> is a track name,\n" +
			"a number, or a path; <plug-in> is a name from `ucdaw plugins`. --preset loads one of\n" +
			"the plug-in's Studio Pro presets by name (a subfolder goes first: Misc/50 Hz Removal).",
		Example: "  ucdaw insert Guitar \"Guitar Rig 7\" --preset Clean",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd, o, daw.Request{Op: "insert", Path: args[0], Name: args[1], Value: preset})
		},
	}
	cmd.Flags().StringVar(&preset, "preset", "", "Studio Pro preset for the plug-in")
	return cmd
}

func newPluginsCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "plugins [filter]",
		Short: "List the effects and instruments Studio Pro has scanned on this Mac",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filter := ""
			if len(args) == 1 {
				filter = args[0]
			}
			res, err := daw.Plugins(filter)
			if err != nil {
				return err
			}
			return printResult(cmd.OutOrStdout(), o, res)
		},
	}
}

func newServeCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Hold one Studio Pro session open so every command answers instantly",
		Long: "serve stays subscribed to Studio Pro, keeps the song model current from Studio\n" +
			"Pro's own updates, and reconnects if Studio Pro restarts. Other ucdaw commands\n" +
			"and `ucdaw mcp` use it automatically while it runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			schema, err := loadSchema(o.schemaPath)
			if err != nil {
				return err
			}
			logw := cmd.ErrOrStderr()
			srv := &dawserve.Server{
				Schema: schema,
				Connect: func(ctx context.Context) (*daw.Session, error) {
					return daw.Connect(ctx, daw.DefaultCacheFile(), o.host)
				},
				Log: func(format string, args ...any) {
					_, _ = fmt.Fprintln(logw, time.Now().Format("15:04:05")+" "+safe(fmt.Sprintf(format, args...)))
				},
			}
			socket := o.socket
			if socket == "" {
				if socket, err = dawserve.DefaultSocket(); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(logw, "ucdaw serve listening on %s\n", socket)
			return srv.Serve(cmd.Context(), socket)
		},
	}
}

// Execute runs the CLI and exits non-zero on failure.
func Execute() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	root := newRootCmd()
	root.SetContext(ctx)
	err := root.Execute()
	cancel()
	if err != nil {
		printError(os.Stderr, err)
		os.Exit(1)
	}
}

func printError(w io.Writer, err error) {
	if errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(w, "Interrupted.")
		return
	}
	var p daw.Problem
	if errors.As(err, &p) {
		_, _ = fmt.Fprintln(w, safe(p.Sentence()))
		if p.Hint != "" {
			_, _ = fmt.Fprintln(w, "  "+safe(p.Hint))
		}
		return
	}
	_, _ = fmt.Fprintln(w, "Error:", safe(err.Error()))
}

// commandArgs parses repeated name=value flags into command arguments.
func commandArgs(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(flags))
	for _, f := range flags {
		name, value, ok := strings.Cut(f, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, daw.Problem{Message: fmt.Sprintf("a command argument is name=value, not %q", f)}
		}
		out[strings.TrimSpace(name)] = value
	}
	return out, nil
}
