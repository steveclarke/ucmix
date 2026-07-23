# ucmix path grammar

The mixer namespace is `group / ch{n} / parameter`, sometimes with a processing
block in between (`line/ch1/comp/threshold`). `{n}` is a 1-based index. This file
is the grammar; run `ucmix dump <prefix>` on a real board for the exact live paths
and index ranges (they vary by model — 16R/32R/32S/64S).

Value forms are marked:

- **on/off** — boolean, write `on`/`off` (`true`/`false` also accepted)
- **dB** — write e.g. `-6dB` or `-6`
- **ms/Hz** — write e.g. `400`, `100Hz`
- **string** — write a quoted string
- **hex** — 6-digit RGB, e.g. `4ed2ff`
- **index** — an integer (input number, enum position)
- **raw** — no humanizing layer; write the wire value the mixer expects, almost
  always a float in `0..1`. `get <path> --raw` reads it; write the same value back.

## Groups

| Group | Typical index | What it is |
|-------|---------------|------------|
| `line/ch{n}` | 1–32 | Input channels (the fat channel: patch, dynamics, EQ, sends) |
| `aux/ch{n}` | 1–16 | Aux / FlexMix buses — the monitor mixes and their masters |
| `fx/ch{n}` | 1–4 | The four internal FX processors (reverb/delay engines) |
| `fxbus/ch{n}` | 1–4 | FX send buses (master + processing for each FX) |
| `fxreturn/ch{n}` | 1–4 | FX returns into mixes |
| `main/ch{n}` | 1 | Main L/R bus |
| `sub/ch{n}` | 1–4 | Subgroups |
| `geq/ch{n}` | per bus | 31-band graphic EQ on a bus |
| `filtergroup/ch{n}` | 1–24 | DCA groups (one fader riding many channels) |
| `mutegroup` | 1–8 | Mute groups |
| `talkback`, `return` | — | Talkback and return channels |
| `global` | — | Mixer-wide: name, stagebox mode, pan/DCA mode |
| `outputpatchrouter`, `stageboxsetup`, `networksetup` | — | Routing / hardware / network |

## `line/ch{n}` — input channel

### Identity & state
| Path | Form | Notes |
|------|------|-------|
| `username` | string | Channel name (scribble strip) |
| `color` | hex | e.g. `4ed2ff` |
| `iconid` | string | e.g. `drums/drumset`, `vocals/leadvocals`, `guitars/bass` |
| `48v` | on/off | Phantom power |
| `mute` | on/off | |
| `solo` | on/off | |
| `volume` | dB | Channel fader |
| `pan` | raw | 0..1, 0.5 = center |
| `polarity` | on/off | Phase invert |
| `link` | on/off | Stereo-link with the adjacent channel (odd+even pair) |

### Input patch / source
| Path | Form | Notes |
|------|------|-------|
| `adc_src` | index | Physical analog input this channel reads (1–32) |
| `avb_src`, `usb_src`, `sd_src` | raw | Source select for AVB/USB/SD input modes |
| `digitalgain` | raw | Digital trim |

### Sends
| Path | Form | Notes |
|------|------|-------|
| `aux{m}` | dB | Send level to aux/monitor mix `m` |
| `aux{m}_pan` | raw | Pan within a stereo aux send (0.5 = center) |
| `assign_aux{m}` | on/off | Assign channel to aux `m` |
| `FXA`…`FXH` | dB | Send level to FX bus A–H |
| `assign_fx{m}` | on/off | Assign channel to FX bus `m` |
| `sub{m}` | on/off | Assign to subgroup `m` |
| `lr` | on/off | Assign to Main L/R |

### Filter / dynamics / EQ (mostly raw)
| Path | Form | Notes |
|------|------|-------|
| `filter/hpf` | Hz* | High-pass filter (*conversion approximate — use raw for exactness) |
| `gate/on` | on/off | |
| `gate/threshold`, `gate/range`, `gate/attack`, `gate/release`, `gate/ratio` | raw | Noise gate |
| `comp/on` | on/off | |
| `comp/threshold`, `comp/ratio`, `comp/attack`, `comp/release`, `comp/gain`, `comp/kneewidth` | raw | Compressor |
| `comp/automode`, `comp/softknee` | on/off | |
| `eq/eqallon` | on/off | Master EQ enable |
| `eq/eqbandon{b}` | on/off | Band `b` enable |
| `eq/eqfreq{b}`, `eq/eqgain{b}`, `eq/eqq{b}`, `eq/eqtype{b}` | raw | Per-band EQ |
| `limit/limiteron` | on/off | |
| `limit/threshold` | dB | |
| `limit/release` | ms | |

## `aux/ch{n}` — monitor mix / master

Same channel-strip shape as `line` (name, color, `link`, `volume`, `comp/*`,
`eq/*`, `limit/*`). For a monitor mix, the useful ones:

| Path | Form | Notes |
|------|------|-------|
| `username` | string | Mix name (shown in QMix) |
| `link` | on/off | Stereo-link odd+even into one stereo mix |
| `volume` | dB | Mix master level |
| `limit/limiteron` | on/off | Hearing-protection limiter on the mix master |
| `limit/threshold` | dB | e.g. `-6` |
| `limit/release` | ms | e.g. `400` (raw default 0.5 = 400 ms) |
| `busmode`, `auxpremode` | raw/index | Bus type and send position (Pre 1 / Pre 2 / Post) |

The per-channel send *into* a mix lives on the source channel: `line/ch{c}/aux{n}`.

## `fx/ch{n}` — FX processor (all raw)

The reverb/delay engine for FX bus `n`. Every plugin knob is a raw `0..1` wire
value; there is no humanizing layer. Reproduce a sound by reading each with
`--raw` and writing it back.

| Path | Form | Notes |
|------|------|-------|
| `type`, `plugin/type` | index | Effect/algorithm select (e.g. plate reverb) |
| `plugin/reflection`, `plugin/size`, `plugin/diffusion`, `plugin/predelay` | raw | Reverb character |
| `plugin/hfdamp_freq`, `plugin/hfdamp_gain`, `plugin/lfdamp_freq`, `plugin/lfdamp_gain`, `plugin/lpf` | raw | Damping / tone |
| `plugin/delay_l`, `plugin/delay_r`, `plugin/fb_l`, `plugin/fb_r`, `plugin/spread` | raw | Delay algorithms |

Channel send into an FX processor: `line/ch{c}/FXA`…`FXH` (dB). FX return level in
a mix: on the `fxreturn` channel within that mix.

## `filtergroup/ch{n}` — DCA group

| Path | Form | Notes |
|------|------|-------|
| `name` | string | DCA name |
| `volume` | dB | The DCA fader (rides its members) |
| `mute` | on/off | |
| `iconid` | string | |

DCA membership assignment is encoded in the group's structure and is not a simple
single-path toggle — inspect `dump filtergroup/ch{n}` on a real board before
scripting it.

## `mutegroup`

| Path | Form | Notes |
|------|------|-------|
| `mutegroup{n}` | on/off | Fire mute group `n` |
| `mutegroup{n}username` | string | Name |
| `allon`, `alloff` | on/off | All mute groups |

## Worked examples

```bash
# Name, color, icon, phantom, patch, stereo-link a channel
ucmix set line/ch1/username "Drums"
ucmix set line/ch1/color 4ed2ff
ucmix set line/ch1/iconid drums/drumset
ucmix set line/ch1/48v off
ucmix set line/ch1/adc_src 1
ucmix set line/ch1/link on

# Build a stereo monitor mix named for a player
ucmix set aux/ch1/username "Steve"
ucmix set aux/ch1/link on

# Hearing-protection limiter on a mix master
ucmix set aux/ch1/limit/limiteron on
ucmix set aux/ch1/limit/threshold -6

# A channel's send into that mix, and into vocal reverb
ucmix set line/ch11/aux1 -6
ucmix set line/ch11/FXA -10

# Copy a raw setting exactly (uncalibrated path)
ucmix get fx/ch1/plugin/reflection --raw     # 0.7907908
ucmix set fx/ch1/plugin/reflection 0.7907908

# Always verify a write with a fresh read
ucmix get line/ch1/48v
```
