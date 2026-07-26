# ucmix

Unofficial Go library and CLI for controlling PreSonus StudioLive Series III
mixers over the **UCNET** network protocol.

> **Unofficial.** ucmix is not affiliated with, authorized, or endorsed by
> PreSonus Audio Electronics, Inc. "PreSonus", "StudioLive", "UC Surface", and
> "UCNET" are trademarks of their respective owners. This project communicates
> with the mixer's network protocol for interoperability only. Field names and
> encodings are reverse-engineered and can differ across firmware revisions.

## What it does

Read and write a StudioLive mixer's state programmatically — channel names,
input patch, 48V, high-pass filters, monitor mixes, sends, limiters, reverb, and
scene recall/store. The headline feature is **board as code**: `verify` and
`apply` an entire mixer configuration from a declarative YAML file instead of
tapping it into UC Surface by hand.

```console
$ ucmix apply front-of-house.yml
applying 47 settings…
applied 47 settings; verify clean
```

## Install

Homebrew (macOS/Linux):

```sh
brew install steveclarke/tap/ucmix
```

Debian/RPM packages are attached to each [release](https://github.com/steveclarke/ucmix/releases).

From source (Go 1.26+):

```sh
go install github.com/steveclarke/ucmix/cmd/ucmix@latest
```

## Quickstart

Find a mixer and save it, then read and write state:

```sh
ucmix setup                            # scan the LAN, pick a board, save it as a profile
                                       #   (or: export UCMIX_HOST=192.168.1.50)

ucmix dump line/ch1                     # every ch1 setting, humanized
ucmix get line/ch1/volume              # -6 dB
ucmix set line/ch1/volume -3dB         # faders speak dB
ucmix set line/ch1/username "Kick"     # names, icons
ucmix set line/ch1/48v on              # phantom power
ucmix set line/ch1/48v=on line/ch1/mute=off   # many writes, one connection
ucmix set -f strip.txt                 # a `path value` per line, one connection
ucmix recall "Main Live" "Opening"     # recall a stored scene
```

Every command accepts `--json` for machine-readable output and `--no-color` for
plain text. Values use human units: `-6dB`, `100Hz`, `on`/`off`, a physical
input number for `adc_src`, a hex string for `color`. Paths use slashes
(`line/ch1/volume`) or dots (`line.ch1.volume`).

A color is written as 6 or 8 hex digits, with or without a leading `#`, and is
always **read back as 8 lowercase RGBA hex digits** (`4ed2ff` → `4ed2ffff`).
That one rendering is what `get`, `dump`, `dump --as-config`, and `verify` all
print, so a color compares equal to itself however it was written.

Writes are read back. `set` and the noun commands re-read every path they wrote
on a fresh connection and report what the board holds, so a value the mixer
clamps or rejects is named rather than reported as a success:

```
✗ set line/ch3/filter/hpf: wrote 2000, board holds 1
```

That exits 1. The read-back costs one extra connection per command, not per
write; `--no-verify` skips it and reports on send. Write commands take their
values verbatim (so `-6dB` is not read as a flag), so `--no-verify` goes before
the path — `ucmix set --no-verify line/ch1/mute on`.

## Noun commands (channel / mix / send)

For the common actions there are noun-grouped shortcuts — a thin veneer over
`set` that needs no path knowledge. Each verb builds one path and writes one
value; anything not covered stays `ucmix set <path> <value>`.

```sh
ucmix channel 3 name "Drums"           # line/ch3/username
ucmix channel 3 patch 5                # line/ch3/adc_src   (physical input)
ucmix channel 3 phantom on             # line/ch3/48v
ucmix channel 3 fader -6dB             # line/ch3/volume
ucmix channel 3 mute on                # line/ch3/mute
ucmix channel 3 color blue             # line/ch3/color     (name or hex)
ucmix channel 3 icon drums             # line/ch3/iconid    (name or id)
ucmix channel 3 hpf 100Hz              # line/ch3/filter/hpf  (24 Hz - 1 kHz)

ucmix mix 1 name "Steve"               # aux/ch1/username
ucmix mix Steve fader -6dB             # a mix answers to its name or its number
ucmix mix 1 stereo on                  # aux/ch1/link
ucmix mix 1 limiter on --threshold -6 --release 400

ucmix send 3 1 -6dB                    # channel 3 into mix 1: line/ch3/aux1
```

Run `ucmix channel --help` or `ucmix mix --help` for the full verb list. Color
names (`blue`, `red`, `green`, …) and icon names (`drums`, `bass`, `vocal`)
resolve to wire values; a hex color or a raw icon id still works.

## Scenes, projects, and scope filters

The mixer stores state in two layers. A **scene** is the mix — fat channel,
mutes, mix levels, FX, DCA and mute groups. A **project** is the setup under it —
input source and patching, AVB/SD/USB routing, flex mode, GEQ, solo. Scenes live
inside a project.

```sh
ucmix ls projects                      # projects on the board
ucmix ls scenes "135 Main Live"        # scenes in one project (title or slot name)
ucmix project ls                       # projects, marking the loaded one

ucmix store "135 Main Live" "Opening"  # store the current state as a new scene
ucmix recall "135 Main Live" "Opening" # recall a stored scene
ucmix rename "135 Main Live" "Opening" "Opening Set"
ucmix delete "135 Main Live" "Soundcheck" --yes
```

`store` allocates the next free scene slot and refuses to overwrite an existing
scene without `--replace`. `store`, `recall` and `delete` report success only
after the board acknowledges the write.

Storing and recalling a **project** as a unit is not implemented: the request UC
Surface sends for it has not been captured, and the preset layer is not a place
to guess.

**Scope filters** decide what a store, recall or reset actually touches — the
blue Project-Filter and Scene-Filter tiles in UC Surface. A tile is either
included in the operation or excluded from it, and a board ships with the scene
filter's `48v` tile excluded, which is why a scene recall leaves phantom power
alone.

```sh
ucmix filters ls                       # every tile in every group
ucmix filters ls scene                 # one group: scene, advanced, project
ucmix filters set scene 48v on         # include phantom power in store/recall
ucmix filters set project inputpatching off
```

The three groups are `scene` (the Scene Filter tiles), `advanced` (Advanced
Scene Filter) and `project` (Project Filter). Tile names are the board's own key
names, so a tile always names the parameter it writes; `-` and `_` are
interchangeable.

## Connecting to a mixer

ucmix resolves which mixer to talk to in this order: the `--host` flag, a named
`--profile`, the `UCMIX_HOST` environment variable, the current saved profile,
and a legacy `host:` in the config file. The UCNET control port `53000` is
assumed when none is given.

**Discovery and setup.** StudioLive mixers announce themselves on the LAN, so
ucmix can find them:

```sh
ucmix discover                         # list mixers on the network
ucmix setup                            # find one, name it, save it as a profile
```

**Profiles.** Save multiple boards and switch between them (front-of-house,
monitors, a rehearsal rig):

```sh
ucmix profile add foh --host 192.168.1.50
ucmix profile add monitor --host 192.168.1.51 --use
ucmix profile ls                       # list, * marks the current one
ucmix profile use foh                  # switch the current profile
ucmix -p monitor dump line/ch1         # use a profile for one command, no switch
```

Profiles live in `~/.config/ucmix/config.yml` (or `$XDG_CONFIG_HOME/ucmix`);
`ucmix config path` prints the location and `ucmix config edit` opens it.

## Board as code

Describe the board in YAML, then verify or apply it. Only the fields you declare
participate — the config is a statement of intent, not a full dump.

```yaml
version: 1

channels:
  1:
    name: Kick
    icon: drums/drumset
    patch: 1            # physical input
    phantom: true       # 48V
    hpf: 80             # Hz, 24-1000 (or `off`)
    fader: -6           # dB
    main: true          # assign to main L/R
    sends:
      Monitor 1: -3     # send level in dB, by mix name

mixes:
  1:
    name: Monitor 1
    stereo: true
    fader: -6
    limiter:
      "on": true
      threshold: -12    # dB
      release: 400      # ms
```

```sh
ucmix verify board.yml          # diff the live board against the file
                                #   exit 0 = clean, 1 = drift, 2 = error
ucmix apply board.yml           # write every declared setting, then verify
ucmix apply board.yml --dry-run # print the ordered write plan, change nothing
ucmix apply board.yml --reset   # factory-reset first (destructive; --yes to skip prompt)
ucmix dump --as-config          # emit the live board as a config file
```

`apply` writes on one connection and verifies on a fresh one — the mixer's
in-session read-back returns unparsed values, so verification requires a new
snapshot.

## Library

```go
import ucmix "github.com/steveclarke/ucmix"

client, err := ucmix.Connect(ctx, "mixer.local:53000")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

client.SetFaderDB(ctx, ucmix.Line, 1, -6)      // −6 dB on channel 1
client.SetName(ctx, ucmix.Line, 1, "Kick")
client.Set48V(ctx, ucmix.Line, 1, true)

level, _ := client.Get("line/ch1/volume")       // humanized read
```

## Protocol

The UCNET wire protocol is documented in [PROTOCOL.md](PROTOCOL.md): packet
framing, message codes, value encodings, tapers, and the JSON scene/preset
commands.

## Calibration status

The fader, limiter-threshold, input-patch, and high-pass-filter conversions are
calibrated against a real StudioLive 32R. The high-pass sweeps 24 Hz to 1 kHz
logarithmically, `pos = ln(Hz / 24) / ln(1000 / 24)`, with position 0 the bottom
of the sweep and how the board stores a filter that is off.

The limiter-release and reverb-type curves are not yet fully characterized —
those fields round-trip on the wire but their human-unit conversions are
provisional. A `raw:` escape hatch in the config accepts wire values directly for
anything uncalibrated.

Humanized reads round to one decimal. A wire position is a 32-bit float, so
inverting one lands a hair off the number that was dialed; `get --raw` shows the
exact position.

## Prior art

- [featherbear/presonus-studiolive-api](https://github.com/featherbear/presonus-studiolive-api)
  — the Node/TypeScript reference implementation this work builds on.
- [samovesel/PreSonus-StudioLive-API](https://github.com/samovesel/PreSonus-StudioLive-API)
  and [martinspinler/osclive](https://github.com/martinspinler/osclive).

## License

MIT — see [LICENSE](LICENSE).
