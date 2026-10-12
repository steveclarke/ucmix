package dawcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveclarke/ucmix/internal/daw"
)

const targetHelp = "average level to bring each stem to, in dBFS"

const targetLong = "The target is an average (RMS) level over the stem's active audio: gaps quieter\n" +
	"than -60 dBFS are left out, so a sparse part is measured by what it plays. -18 dBFS\n" +
	"is the usual gain-staging level: digital consoles and hardware-modeled plug-ins treat\n" +
	"it as 0 VU, and it leaves about 18 dB above the average for peaks. Stems at a common\n" +
	"average start the mix with every fader near 0 dB."

func newAnalyzeCmd(o *options) *cobra.Command {
	var target float64
	cmd := &cobra.Command{
		Use:   "analyze [file|folder|track]...",
		Short: "Measure stems: average and peak level, loudness, silence, clipping, and the trim each needs",
		Long: "Measures each stem the way a mix engineer does before touching a level:\n\n" +
			"  average   RMS level in dBFS over the stem's active audio\n" +
			"  peak      highest sample, in dBFS\n" +
			"  LUFS      integrated loudness (ITU-R BS.1770-4, as streaming services measure)\n" +
			"  active    how long the stem plays above -60 dBFS\n" +
			"  silent    a stem that peaks below -60 dBFS is an empty export\n" +
			"  clipping  runs of samples at full scale (or over it, in float files)\n" +
			"  trim      the input trim that brings the average to the target (-24 to +24 dB)\n\n" +
			targetLong + "\n\n" +
			"Files and folders are measured on this Mac without Studio Pro. A track name\n" +
			"measures the file imported onto that track; no arguments measures every audio track\n" +
			"in the open song. Reading only: nothing in the song changes.",
		Example: "  ucdaw analyze ~/Music/stems\n  ucdaw analyze Bass Drums\n  ucdaw analyze\n  ucdaw analyze Vox.wav --target -20",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daw.CheckTarget(target); err != nil {
				return err
			}
			files, tracks, err := splitAnalyzeArgs(args)
			if err != nil {
				return err
			}
			if len(files) > 0 && len(tracks) == 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), analyzeTimeout)
				defer cancel()
				res, err := daw.Analyze(ctx, files, target)
				if err != nil {
					return err
				}
				return printResult(cmd.OutOrStdout(), o, res)
			}
			req := daw.Request{Op: "analyze", Files: files, Tracks: tracks}
			if cmd.Flags().Changed("target") {
				req.TargetDBFS = target
			}
			return run(cmd, o, req)
		},
	}
	cmd.Flags().Float64Var(&target, "target", daw.DefaultTarget, targetHelp)
	return cmd
}

func newGainStageCmd(o *options) *cobra.Command {
	var target float64
	cmd := &cobra.Command{
		Use:   "gainstage [track]...",
		Short: "Set each track's input trim so its stem sits at the target average; faders untouched",
		Long: "Measures the stem on each audio track (or the named tracks) and sets the track's\n" +
			"input trim so the stem averages the target, the way an engineer gain-stages before\n" +
			"mixing. Faders, mutes and pans are not touched, so the balance the musician set\n" +
			"stays. A silent stem (an empty export) and a track without a stem are left alone and\n" +
			"named. A stem more than 24 dB off reaches the trim's limit; the answer says how far\n" +
			"short it is.\n\n" + targetLong,
		Example: "  ucdaw gainstage\n  ucdaw gainstage Bass Keys\n  ucdaw gainstage --target -20",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := daw.CheckTarget(target); err != nil {
				return err
			}
			req := daw.Request{Op: "gainstage"}
			if len(args) > 0 {
				req.Tracks = args
			}
			if cmd.Flags().Changed("target") {
				req.TargetDBFS = target
			}
			return run(cmd, o, req)
		},
	}
	cmd.Flags().Float64Var(&target, "target", daw.DefaultTarget, targetHelp)
	return cmd
}

// analyzeTimeout bounds measuring files on this Mac: 64 stems of an hour each.
const analyzeTimeout = 10 * time.Minute

// splitAnalyzeArgs sorts arguments into files (anything that exists, looks
// like a path or has an audio file's extension) and track names.
func splitAnalyzeArgs(args []string) (files, tracks []string, err error) {
	for _, a := range args {
		t := strings.TrimSpace(a)
		if t == "" {
			continue
		}
		statErr := os.ErrNotExist
		if abs, err := daw.ImportPaths([]string{t}); err == nil && len(abs) == 1 {
			_, statErr = os.Stat(abs[0])
		}
		ext := strings.ToLower(filepath.Ext(t))
		if statErr == nil || strings.ContainsAny(t, "/\\") || strings.HasPrefix(t, "~") || ext == ".wav" || ext == ".aif" || ext == ".aiff" || ext == ".aifc" || ext == ".wave" || ext == ".bwf" {
			files = append(files, t)
			continue
		}
		tracks = append(tracks, t)
	}
	if len(files) > 0 {
		if files, err = daw.ImportPaths(files); err != nil {
			return nil, nil, err
		}
	}
	return files, tracks, nil
}
