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

Point ucmix at the mixer, then read and write state. The mixer host comes from
`--host`, the `UCMIX_HOST` environment variable, or `~/.config/ucmix/config.yml`
(in that order); the UCNET control port `53000` is assumed when none is given.

```sh
export UCMIX_HOST=mixer.local          # or 192.168.1.50

ucmix dump line/ch1                     # every ch1 setting, humanized
ucmix get line/ch1/volume              # -6 dB
ucmix set line/ch1/volume -3dB         # faders speak dB
ucmix set line/ch1/username "Kick"     # names, icons
ucmix set line/ch1/48v on              # phantom power
ucmix recall "Main Live" "Opening"     # recall a stored scene
```

Every command accepts `--json` for machine-readable output and `--no-color` for
plain text. Values use human units: `-6dB`, `100Hz`, `on`/`off`, a physical
input number for `adc_src`, a hex string for `color`.

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
    hpf: 80             # Hz
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

The fader, limiter-threshold, and input-patch conversions are calibrated against
a real StudioLive 32R. The high-pass-filter (0..1 → Hz), limiter-release, and
reverb-type curves are not yet fully characterized — those fields round-trip on
the wire but their human-unit conversions are provisional. A `raw:` escape hatch
in the config accepts wire values directly for anything uncalibrated.

## Prior art

- [featherbear/presonus-studiolive-api](https://github.com/featherbear/presonus-studiolive-api)
  — the Node/TypeScript reference implementation this work builds on.
- [samovesel/PreSonus-StudioLive-API](https://github.com/samovesel/PreSonus-StudioLive-API)
  and [martinspinler/osclive](https://github.com/martinspinler/osclive).

## License

MIT — see [LICENSE](LICENSE).
