---
name: ucmix
description: Control a PreSonus StudioLive Series III mixer from the command line. Trigger when the user asks about ucmix, a StudioLive / 32R / UCNET mixer, setting channel names/patches/48V/faders/mutes, monitor (aux) mixes, limiters, FX/reverb, stereo links, colors/icons, scenes, board-as-code (verify/apply a config), or reading/writing any mixer parameter.
---

# ucmix — StudioLive mixer control

`ucmix` reads and writes any parameter on a PreSonus StudioLive Series III mixer
over the mixer's own network protocol (UCNET). No PreSonus software is required
or involved — it talks straight to the mixer's control port.

## First checks

```bash
command -v ucmix
ucmix --version
ucmix profile ls        # is a mixer saved?
```

If `ucmix` is missing, install it:

```bash
brew install steveclarke/tap/ucmix
```

If no mixer is configured, find and save one (mixers announce themselves on the LAN):

```bash
ucmix discover          # list mixers on the network
ucmix setup             # interactive: pick one, name it, save it as the current profile
# or, if you already know the address:
ucmix profile add foh --host 192.168.1.50 --use
```

Every command then uses the current profile. Override per-command with `-p <name>`
or `--host <ip>`.

## The model — two verbs over one namespace

The mixer is one flat namespace of ~20,000 parameters, each a `path → value`:

```
line/ch1/username        = "Drums"      # channel 1 name
line/ch1/48v             = true         # phantom power
line/ch1/volume          = -6           # fader, dB
aux/ch1/limit/threshold  = -6           # monitor-mix limiter, dB
fx/ch1/plugin/reflection = 0.79         # a reverb knob (raw)
```

Everything the mixer can do is one of those paths. Two verbs cover all of it:

- `ucmix get <path>` — read one value
- `ucmix set <path> <value>` — write one value; also `set p1=v1 p2=v2 …` or
  `set -f <file>` (a `path value` per line) to write many over one connection

Read `reference/paths.md` for the path grammar (groups, channel indexing, and the
value form for each parameter family). To see the exact live paths on a specific
board, run `ucmix dump` (all) or `ucmix dump <prefix>` (filtered).

## Humanized vs raw values

Common controls accept **human values** and the tool converts to the wire form:

| Path family | You write | Not the raw wire value |
|-------------|-----------|------------------------|
| `.../48v`, `.../mute`, `.../*on*`, `.../link` | `on` / `off` | (bool) |
| `.../volume`, `.../aux{n}`, `.../FXA`..`FXH`, `.../limit/threshold` | dB, e.g. `-6dB` | |
| `.../limit/release` | ms, e.g. `400` | |
| `.../username` | a string, e.g. `"Vox Steve"` | |
| `.../color` | hex, e.g. `4ed2ff` | |
| `.../iconid` | an icon id, e.g. `vocals/leadvocals` | |
| `.../adc_src` (input patch) | the input number, e.g. `5` | |

Every **other** path has no humanizing layer — you pass the **raw wire value the
mixer expects**, usually a float in `0..1` (e.g. `fx/ch1/plugin/lpf 0.869`), an
integer index, or an enum number. `get <path>` returns the raw value; `get <path>
--raw` forces raw even on humanized paths. When reproducing a captured setting,
read its raw value and write that same raw value back.

## Commands

Run `ucmix <command> --help` for flags; the CLI evolves, so verify against `--help`
rather than trusting this list to be complete.

- `get <path>` / `set <path> <value>` — read / write one parameter
- `set p1=v1 p2=v2 …` / `set -f <file>` — write many parameters over one connection
- `channel <n> <verb> <value>` / `mix <name|n> <verb> <value>` / `send <ch> <mix> <dB>` —
  human shortcuts over `set` for the common channel-strip, monitor-mix, and send
  actions (a thin veneer; the raw `set` path model still covers everything). Built
  for humans at a keyboard — an agent should keep using raw `get`/`set` below.
- `dump [prefix]` — read every path (or those under a prefix); `--as-config` emits YAML
- `verify <config.yml>` / `apply <config.yml>` — board as code: diff / write a whole config
- `recall <project> <scene>` / `store <project> <scene>` — mixer scenes
- `reset` — factory reset (destructive; needs `--yes`)
- `ls` — list stored presets
- `discover` / `setup` / `profile` / `config` — find/save/manage mixer connections

## Agent rules

- Use `--json` for any command whose output you will parse; `--no-color` for plain text.
- **Verify writes with a fresh `get`.** `set` reports that it sent the value, not that
  the board is now in that state. Read it back to confirm.
- Prefer humanized values (`-6dB`, `on`, an input number) where a path family supports
  them; fall back to raw `0..1` wire values for everything else.
- To copy a setting from one board/state to another, `get <path> --raw` then
  `set <path> <that raw value>` — raw round-trips exactly.
- Write many parameters in one call — `set p1=v1 p2=v2 …` or `set -f <file>` — rather
  than looping `set`. A batch reuses one connection and commits once; separate `set`
  processes reconnect per write and can drop writes under rapid reconnect.
- `reset` and `apply --reset` are destructive — only with `--yes` and a clear target.
- Never assume a path exists; confirm with `dump <prefix>` or `get` on a real board.

## Known limitations

- `ls` / project listing may not work on some real boards: the board may not answer the
  preset-list request. It now fails with a clear timeout and hint instead of hanging.
- `apply` writes over one connection with a library commit barrier and verifies on a
  fresh connection (a fresh `get`), so it no longer depends on the project-list request
  that can go unanswered. `set -f <file>` is the same batch write path without the verify.
- HPF (Hz), limiter release curve, and reverb-type enums are not fully calibrated —
  their humanized conversions are approximate. Use raw values when exactness matters.
- Some UCNET parameters have **no control in UC Surface** (e.g. an FX return's Main/LR
  assign, `fxreturn/chN/lr`). Writing one leaves the board in a state the operator cannot
  see or undo from the console. Prefer a change that maps to a visible UC Surface control
  (e.g. pull the FX return fader down for a dry main, not an LR unassign), and when a write
  has no UI equivalent, say so.
