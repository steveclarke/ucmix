---
name: ucdaw
description: Drive Fender Studio Pro 8 (formerly Studio One) over UCNET, the Studio Pro Remote app's protocol. Trigger when the user asks to play, stop, loop, set the click or tempo, add a track, put a plug-in (Guitar Rig, Pro EQ…) on a track with a preset, rename/mute/solo/fader a channel, undo, run any Studio Pro menu or key command, or read what is in the open song — or mentions ucdaw, Studio Pro, Studio One, or the studio-pro-ucnet MCP tools.
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
ucdaw transport play --bars 4        # play 4 bars, then stop
ucdaw transport play --for 2s        # play 2 seconds, then stop
ucdaw transport play | stop | rtz    # plain play keeps going until stop
ucdaw transport loop on|off          # needs a loop range in the song
ucdaw click on|off                   # metronome
ucdaw tempo 96                       # bpm; `ucdaw tempo` reads it
ucdaw track add mono --name Guitar   # mono or stereo audio track
ucdaw insert Guitar "Guitar Rig 7" --preset Clean
ucdaw plugins rig                    # installed effects and instruments
ucdaw state                          # song summary; `ucdaw state mixer/channel/ch1` for every value
```

## Anything else: Studio Pro's own commands

Studio Pro advertises its whole command list (about 1,450 commands in 52
categories: every menu item and key command) to the remote. Run any of them:

```bash
ucdaw command list undo
ucdaw command run "Edit > Undo"
ucdaw command run Track "Add Audio Track (stereo)"
ucdaw command run "Zoom > Zoom Full"
```

ucdaw reports what changed ("removed Guitar Rig 7 from Guitar"). View and
editing commands change nothing Studio Pro reports; the answer says so.
Commands act on the current selection, like their menu items.

If a command opens a dialog (it needs choices), ucdaw cancels the dialog at
once and says so, so Studio Pro is never left blocked.

## Parameters by path

The full remote object model (from Studio Pro's own `dawremote.xml`) is listed
by `ucdaw params [filter]`, one line per pattern (`mixer/channel/ch<n>/mute`).

```bash
ucdaw param get mixer/channel/ch1/volume
ucdaw param set mixer/channel/ch1/mute on
ucdaw param set mixer/channel/ch1/label "Lead Vox"
ucdaw param set mixer/channel/ch1/volume 60      # fader travel, 0–100
```

Values are in the parameter's own units: on/off for switches, bpm for tempo,
list entries by name. Fader volume is fader travel in percent; Studio Pro does
not publish its dB curve.

## MCP tools (`studio-pro-ucnet`)

`transport`, `click`, `tempo`, `get_song`, `get_values`, `get_param`,
`set_param`, `list_params`, `add_track`, `add_insert`, `list_plugins`,
`list_commands`, `run_command`. Start with `get_song`. Undo is
`run_command` Edit / Undo.

## Rules

- Never save, record, arm, open, close or switch songs, or quit Studio Pro,
  unless the musician asks for exactly that.
- After a test that changes the song, undo it.
- Plug-in presets are Studio Pro presets, named relative to the plug-in's
  preset folder: `Misc/50 Hz Removal`. A preset that is not found leaves the
  plug-in on default settings and the answer says so.
- `--json` on any command gives structured output.
