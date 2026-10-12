---
name: ucdaw
description: Drive Fender Studio Pro 8 (formerly Studio One) over UCNET, the Studio Pro Remote app's protocol. Trigger when the user asks to start a new song, play, stop, loop, set the click or tempo, import audio files or a folder of stems (assemble a mix from stems), add an audio or instrument track, put a plug-in (Guitar Rig, Pro EQ…) on a track with a preset, edit the arrangement (add, repeat, move, delete, rename sections), edit audio (fade, split, trim, crossfade, quantize), set a fader or input trim in dB, measure stems (levels, loudness, silent or clipped exports), gain-stage, mix or rebalance a song from the musician's ear notes ("drums a touch loud", "can't hear the bass"), remember a favorite sound, rename/mute/solo a channel, undo, run any Studio Pro menu or key command, or read what is in the open song — or mentions ucdaw, Studio Pro, Studio One, or the studio-pro-ucnet MCP tools.
---

# ucdaw — Studio Pro over UCNET

`ucdaw` talks to Studio Pro the way the Studio Pro Remote iPad app does: over
the local network, with Studio Pro in the background. Every change is read back
from Studio Pro before ucdaw reports it, and every answer is one plain line.

The same engine is exposed three ways:

- the `ucdaw` CLI,
- `ucdaw serve`, a background process that holds one session open (answers in
  about 40 ms instead of 0.4–3 s),
- `ucdaw mcp`, registered in Claude Code as `studio-pro-ucnet`.

## First checks

```bash
command -v ucdaw          # installed? (go install ./cmd/ucdaw from the ucmix repo)
ucdaw discover            # is Studio Pro on the network?
ucdaw state               # what is in the song?
```

If `discover` finds nothing: Studio Pro must be open with a song, and its
Remote service on. Keep `ucdaw serve` running in a terminal for instant answers.
`--host <ip>` (or `UCDAW_HOST`) trusts only Studio Pro at that address.

## Musician-level commands

```bash
ucdaw song new --tempo 72 --click on # new empty song; the open one stays in its tab
ucdaw transport play --bars 4        # play 4 bars, then stop
ucdaw transport play --for 2s        # play 2 seconds, then stop
ucdaw transport play | stop | rtz    # plain play keeps going until stop
# A timed play that someone stops (or restarts) in Studio Pro is left alone.
ucdaw transport loop on|off          # needs a loop range in the song
ucdaw click on|off                   # metronome
ucdaw tempo 96                       # bpm; `ucdaw tempo` reads it
ucdaw track add mono --name Guitar   # mono or stereo audio track
ucdaw track add --name Guitar --insert "my clean guitar"   # with a plug-in or favorite
ucdaw insert Guitar "Guitar Rig 7" --preset Clean
ucdaw instrument add "Syntronik 2" --name Pad [--preset P]  # instrument track, preset read back
ucdaw fader Pad -6 | --down 3 | --up 2                      # dB, on Studio Pro's own curve
ucdaw plugins rig                    # installed effects and instruments
ucdaw presets "Pro EQ" [filter]      # a plug-in's Studio Pro presets
ucdaw state                          # song summary; `ucdaw state mixer/channel/ch1` for every value
```

## Mixing: measure, trim, gain-stage

**Read [mixing.md](mixing.md) before changing any level.** It is the
engineer's workflow: measure first, trim fixes the stem, faders set the
balance, never normalize to peak, never reset the musician's balance without
asking, ear notes become small dB moves, every change reported with numbers.

```bash
ucdaw analyze                          # every audio track's stem in the open song
ucdaw analyze Bass Drums               # the stems imported onto these tracks
ucdaw analyze ~/Music/stems            # files or folders; no Studio Pro needed
ucdaw trim Bass 20 | -6 | --up 3       # input trim (Input Gain), -24..+24 dB
ucdaw gainstage [tracks] [--target -18]  # every trim to the target; faders untouched
```

`analyze` reports per stem: average (RMS over active audio; gaps below
-60 dBFS left out), peak, integrated LUFS (ITU-R BS.1770), active time, silent
("Loops is silent: an empty export"), clipping, and the trim that brings it to
the -18 dBFS target. A track name is resolved from ucdaw's import log, or from
the song's waveform cache plus Spotlight (checked by length in frames).
Negative levels work as plain arguments: `ucdaw fader Drums -3`.

## Starting a new song

```bash
ucdaw song new                        # the New window's current template and settings
ucdaw song new --tempo 72 --click on  # "a new song at 72 with the click on"
```

Runs File > New, presses OK in Studio Pro's New window (found by its label),
waits until Studio Pro reports the new song's name (the date and the user's name,
"(2)" for a second one that day), then sets tempo and click and reads them back:
"Started a new song (2026-10-12 Stephen Clarke) at 72 bpm, click on." The song
that was open stays open in its own tab; nothing is saved or closed. serve
follows the new song without reconnecting. If Studio Pro shows any other window
instead, ucdaw closes it with its Cancel (or No) button and says so, or leaves it
open for the musician when no button just cancels. `command run "File > New Session"`
only cancels the New window and points here.

## Arrangement (arranger sections)

```bash
ucdaw section                         # Verse bar 1 (8 bars), Chorus bar 9 …
ucdaw section add Verse Chorus Bridge [--bars 8]   # appended at the end
ucdaw section repeat Chorus after Bridge           # "repeat chorus one after the bridge"
ucdaw section duplicate Chorus                     # copy right after itself
ucdaw section move Bridge before "Chorus 2"        # two undo steps
ucdaw section delete Bridge                        # gap closes
ucdaw section rename 1 Intro
ucdaw section play Chorus --bars 4                 # "play from the chorus"
```

Sections are named by name, number, or name and occurrence ("Chorus 2",
"second chorus", "last chorus"). Everything in a section's time moves with
it. Each edit is confirmed from the arranger list Studio Pro sends back.

## Audio edits

```bash
ucdaw audio fade-out Guitar [--bars 1]   # "crossfade the end of the take"
ucdaw audio fade-in Guitar
ucdaw audio split Guitar 9               # at bar 9
ucdaw audio trim-end Guitar 17
ucdaw audio crossfade Guitar [--ms 10]
ucdaw audio quantize Guitar
```

They act on every event of the track. Studio Pro does not report audio events
to remote apps, so these are sent but not read back, and the answer says so.

## Importing stems

```bash
ucdaw import ~/Music/stems                 # every WAV/AIFF in the folder, in name order
ucdaw import Kick.wav Snare.wav            # files
ucdaw import Vox.wav --onto Vox --at 17    # onto an existing track, at bar 17
```

Each file gets its own new track, mono or stereo to match the file, named for a
musician ("01_Kick_Final.wav" → Kick, "03-Pad (Stereo) v2.wav" → Pad; a prefix
every file shares, like the song name, is dropped), with its clip at bar 1 and
the clip renamed to match. Files go in one at a time; other files in the folder
are skipped and named in the answer.

What is read back: each track (created, named, selected, mono or stereo). What is
not: the clip's position, because Studio Pro does not report clips to remotes.
ucdaw knows Studio Pro read each file from the waveform it draws into the song's
`Cache/Images` folder; a file the song has seen before gets a two-second wait
instead. Pass the "not read back" part on to the musician.

- Studio Pro must be on this Mac: ucdaw hands it each file with `open`.
- Undo is about three `Edit > Undo` presses per stem; press it until `get_song`
  no longer lists the tracks.
- A conversion question from Studio Pro is accepted (the musician wants the
  stems in); any other dialog is closed with its Cancel button and reported, or
  left open for the musician when no button just cancels.

## Favorites

```bash
ucdaw favorite set "clean guitar" "Guitar Rig 7" --preset Clean
ucdaw favorite set "worship synth" "Syntronik 2"
ucdaw favorite list | remove <name>
```

`insert`, `track add --insert` and `instrument add` take a favorite wherever
they take a plug-in ("my clean guitar" works). A preset must be a Studio Pro
preset: a sound saved only in Guitar Rig's own browser has to be saved once
from the preset menu at the top of the plug-in window in Studio Pro.

## Anything else: Studio Pro's own commands

Studio Pro advertises its whole command list (about 1,450 commands in 52
categories: every menu item and key command) to the remote. Run any of them:

```bash
ucdaw command list undo
ucdaw command run "Edit > Undo"
ucdaw command run Track "Add Audio Track (stereo)"
ucdaw command run "Zoom > Zoom Full"
ucdaw command run Transport "Forward Bar" --arg Bars=4   # arguments, as macros carry them
```

ucdaw reports what changed ("removed Guitar Rig 7 from Guitar"). View and
editing commands change nothing Studio Pro reports; the answer says so.
Commands act on the current selection, like their menu items.

If a command opens a dialog (it needs choices), ucdaw presses its Cancel (or
No, Don't…) button at once and says so, so Studio Pro is not left blocked. It
never chooses for the musician: a dialog with no such button (Save / Don't Save,
Yes / OK) stays open and the answer names it and its buttons; ask the musician
to answer it in Studio Pro.

## Parameters by path

The full remote object model (from Studio Pro's own `dawremote.xml`) is listed
by `ucdaw params [filter]`, one line per pattern (`mixer/channel/ch<n>/mute`).

```bash
ucdaw param get mixer/channel/ch1/volume
ucdaw param set mixer/channel/ch1/mute on
ucdaw param set mixer/channel/ch1/label "Lead Vox"
ucdaw param set mixer/channel/ch1/volume -6      # dB; "60%" sets fader travel
```

Values are in the parameter's own units: on/off for switches, bpm for tempo,
dB for faders, sends and cue mixes, list entries by name.

## MCP tools (`studio-pro-ucnet`)

`get_song`, `new_song` (tempo, click), `analyze_audio`, `trim`, `gain_stage`, `transport`, `click`, `tempo`, `add_track`, `add_insert`,
`add_instrument`, `import_audio`, `section`, `audio_edit`, `fader`, `favorite`,
`list_plugins`, `list_presets`, `get_values`, `get_param`, `set_param`,
`list_params`, `list_commands`, `run_command` (with `args`). Start with
`get_song`. Undo is `run_command` Edit / Undo.

## Rules

- Never save, record, arm, open, close or switch songs, or quit Studio Pro,
  unless the musician asks for exactly that. Start a new song (`song new`,
  `new_song`) only when they ask for one.
- After a test that changes the song, undo it. Tempo is not in Studio Pro's
  undo history; set it back. A fader move is not reliably undone either; set
  the old level back.
- Plug-in presets are Studio Pro presets, named relative to the plug-in's
  preset folder: `Misc/50 Hz Removal`. A preset that is not found leaves the
  plug-in on default settings and the answer says so.
- `--json` on any command gives structured output.
