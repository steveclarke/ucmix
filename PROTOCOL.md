# UCNET Protocol Reference

UCNET is the network control protocol used by PreSonus StudioLive Series III mixers
(rack and console). Control clients such as UC Surface and QMix speak it to read and
write mixer state — channel names, mutes, faders, aux sends, effects, scene recall, and
more.

This document is a reverse-engineered, unofficial reference. It is **not affiliated with,
authorized by, or endorsed by PreSonus**. Protocol details were derived from prior
open-source work (see [Prior art](#prior-art)) and from live probing of a Series III
mixer. Field names and encodings can differ across firmware revisions; treat everything
here as observed behavior, not a specification.

## Transport

| Purpose | Transport | Port |
|---------|-----------|------|
| Control | TCP | `53000` |
| Discovery | UDP | `47809` |
| Metering | UDP | client-chosen (advertised in the `Hello` packet) |

Multiple control clients can connect to a mixer at once; a write from one client
broadcasts to all connected clients within a few tens of milliseconds.

## Connection & handshake

1. **TCP connect** to `mixer:53000`.
2. **Send `Hello`**, advertising the local UDP port the client will listen on for metering.
3. **Send `Subscribe`** as a JSON body. The subscribe request looks like:
   ```json
   {
     "id": "Subscribe",
     "clientName": "ucmix",
     "clientInternalName": "ucmix",
     "clientType": "StudioLive API",
     "clientDescription": "User",
     "clientIdentifier": "0000000000000000",
     "clientOptions": "perm users levl redu rtan",
     "clientEncoding": 23106
   }
   ```
   `clientIdentifier` is an arbitrary per-client identity string — any stable value works.
4. **Board replies with `ZB`**, a zlib-compressed snapshot of the full mixer state.
5. **Board streams live deltas** as `PV` / `PC` / `PS` messages that override the snapshot.
6. **Keep-alive.** The client sends a periodic `KA` message to hold the subscription open.
   A subscription that goes stale stops receiving live deltas until the client reconnects.

A complete client therefore parses the `ZB` snapshot into a state tree, then applies the
`PV`/`PC`/`PS` deltas on top of it.

## Packet framing

Every packet has the same frame:

```
[ header 4 bytes ][ length 2 bytes LE ][ message code 2 bytes ][ connIdentity 4 bytes ][ payload ]
```

| Field | Bytes | Notes |
|-------|-------|-------|
| header | 4 | `55 43 00 01` — ASCII `"UC"` followed by `00 01` |
| length | 2 | little-endian uint16 = `len(messageCode) + len(connIdentity) + len(payload)` = payload length + 6 |
| message code | 2 | two ASCII characters (e.g. `PV`, `PS`, `ZB`) |
| connIdentity | 4 | request/response correlation token |
| payload | n | begins at byte offset 12 |

**connIdentity.** Four bytes laid out as `[A, 0x00, B, 0x00]`, where `A` and `B` are
single bytes. It is used to correlate requests with responses. Its value varies between
clients and can be overridden per request. Receivers must not assume a fixed value —
match on the connIdentity a request was sent with rather than hard-coding one.

## Message codes

| Code | Meaning | Payload shape |
|------|---------|---------------|
| `PV` | Param value — bool or float set/update | `key\0\0\0` + 4-byte LE float (bool = `1.0`/`0.0`) |
| `PC` | Param chars — e.g. color | `key\0\0\0` + raw bytes (color = hex digits + alpha byte) |
| `PS` | Param string — names, icon IDs | `key\0\0\0` + UTF-8 + trailing `\0` |
| `MS` | Fader positions, bulk | per-type arrays of linear positions |
| `ZB` | Full state snapshot | zlib-compressed nested tree |
| `FD` | File / list data | scene and preset list transfers |
| `CK` | Housekeeping | — |
| `JM` | JSON message — presets, scenes, reset | 4-byte LE JSON length + JSON body |
| `KA` | Keep-alive | empty (length 6) |
| `Hello` | Handshake | advertises the client's UDP metering port |

## Value encodings

- **float** — 4-byte little-endian IEEE-754.
- **bool** — a float, `1.0` (true) or `0.0` (false).
- **string** — UTF-8 with a single trailing null byte.
- **color** — hex color digits followed by an alpha byte, carried in a `PC` message.
- **key separator.** In `PV`/`PC`/`PS` payloads the key is followed by a null terminator
  and a 2-byte "part A" field, i.e. `key` + `\x00` + two bytes. The two bytes are normally
  `00 00`; `00 01` has been observed on some filter-group deltas.
- **`JM` JSON body** — the 2-char code and connIdentity are followed by a 4-byte
  little-endian length and then the JSON text. Parsing is whitespace-insensitive.

### State model

State is a nested tree keyed by `/`-delimited paths (also addressable with `.`). The `ZB`
snapshot provides the initial full tree; `PV`/`PC`/`PS` deltas mutate individual leaves.

Channel types are addressed by type and index, e.g. `line/ch{N}` for input channels,
`aux/ch{N}` for aux (monitor) buses, `fxbus/ch{N}` and `fxreturn/ch{N}` for effects, and
`fx/ch{N}` for effects processors. Stereo-linked buses occupy consecutive indices, with
the odd index as the master of the pair (e.g. `aux/ch1` master + `aux/ch2` slave).

Representative writable keys, all relative to a channel path such as `line/ch{N}`:

| Purpose | Key | Type |
|---------|-----|------|
| Mute | `mute` | bool (PV) |
| Solo | `solo` | bool (PV) |
| Fader level | `volume` | float 0..1 (PV) |
| Pan | `pan` | float (PV) |
| Aux / monitor send | `aux{M}` | float 0..1 (PV) |
| Channel color | `color` | hex+alpha (PC) |
| Stereo link | `link`, `linkmaster`, `panlinkstate` | bool (PV) |
| Label / name | `username` | string (PS) |
| Icon | `iconid` | string (PS), e.g. `drums/drumset` |
| 48V phantom | `48v` | bool (PV) |
| High-pass filter | `filter/hpf` | float 0..1 (PV), `0` = off |
| Assign to Main LR | `lr` | bool (PV) |
| Input patch | `adc_src` | float = input ÷ 32 (PV) |
| Preamp gain | `preampgain` | float 0..1 (PV) |
| Polarity | `polarity` | bool (PV) |
| Effects send | `FX{A..H}`, `assign_fx{1..8}` | float 0..1 / bool (PV) |
| EQ band | `eq/eqgain{1..6}`, etc. | float (PV) |
| Compressor | `comp/{on,threshold,ratio,attack,release,gain}` | float / bool (PV) |

Aux buses carry their own fader, name, link, send-position mode, and limiter keys under
`aux/ch{N}` (e.g. `volume`, `username`, `auxpremode`, `limit/limiteron`,
`limit/threshold`, `limit/release`). Effects returns carry `username`, per-mix send
`aux{M}`, and `mute` under `fxreturn/ch{N}`, and the processor type under `fx/ch{N}/type`.

## Tapers & scaling

Many parameters are normalized `0..1` floats that require conversion to reach human units.

- **Fader / volume.** A `*/volume` field is stored on the wire as a normalized `0..1`
  float. Note a display quirk: some client APIs *report* volume on a `0..100` scale, so a
  value read as `74.6` corresponds to a wire value of `0.746` — divide the display value by
  100 before writing, or a raw `74.6` pins the fader to the top. The dB curve is
  logarithmic over roughly `-84 dB` (bottom) to `+10 dB` (top). One reverse-engineered
  mapping from dB to a `0..100` position is:

  ```
  pos(db) = trunc(72.5204177782
                  + 2.473473992    * db
                  + 0.026567557    * db^2
                  + 0.0000880866   * db^3)      clamped to [0,100]
  ```

  with `-84 dB → 0` and `+10 dB → 100` as the endpoints. Divide by 100 for the `0..1` wire
  value. Known calibration point: `0.746 ≈ −6 dB`.
- **Limiter threshold** (`aux/ch{N}/limit/threshold`) — linear over the range
  `−28..0 dB`. `1.0` = `0 dB` (effectively off); `0.786 ≈ −6 dB`.
- **Input patch** (`adc_src`) — the physical input number divided by 32. To read the
  physical input: `round(adc_src * 32)`. Example: `0.78125 = 25/32` → physical input 25;
  `1.0 = 32/32` → input 32.
- **Limiter release** (`limit/release`) — normalized; approximate mapping `0.5 ≈ 400 ms`.
  The full curve is **not yet fully characterized**.
- **High-pass filter** (`filter/hpf`) — normalized `0..1`, `0` = off. The `0..1 → Hz`
  taper is **not yet decoded**.
- **Effects type** (`fx/ch{N}/type`) — a normalized enum; distinct algorithms map to
  distinct fractional values.

## JM commands — presets, scenes, reset

Scene, project, and preset operations travel as JSON inside a `JM` message. Paths below
use generic placeholders — substitute real project and scene file names.

**Recall** an existing preset / scene / project:
```json
{ "id": "RestorePreset", "url": "presets", "presetTarget": "",
  "presetFile": "presets/proj/<PROJECT>.proj/<NN>.<name>.scn" }
```

**Store** the current state to a preset / scene / project (mirror of `RestorePreset`):
```json
{ "id": "StorePreset", "url": "presets", "presetTarget": "",
  "presetFile": "presets/proj/<PROJECT>.proj/<NN>.<name>.scn" }
```

**List** stored presets under a namespace (e.g. `presets/proj`, `presets/channel`):
```json
{ "id": "Listpresets", "url": "presets/proj" }
```

**Reset** the mixer. The two scope flags control what is cleared:
```json
{ "id": "ResetMixer", "resetSceneSettings": 1, "resetProjectSettings": 0,
  "url": "presets", "src": "presets" }
```
- `resetSceneSettings: 1` clears scene-level settings.
- `resetProjectSettings: 1` clears project-level settings.
- **Both flags = 1 is a full factory wipe** — names, input patch, and all mixes are cleared.

## Gotchas

- **Write pacing.** Sending several thousand writes in one burst drops the TCP connection
  and loses the tail. Pace writes — a small delay every ~40 frames avoids the drop. A robust
  client needs flow control.
- **Fresh-connection read-back rule.** After writing a key that has no registered value
  transformer, an in-session read may return the raw buffer and parse as `NaN`. A fresh
  connection reads the correctly parsed value from the `ZB` snapshot. Verify writes on a
  fresh connection.
- **Volume scale trap.** See [Tapers & scaling](#tapers--scaling): `*/volume` wants a
  `0..1` wire value even though some APIs surface it on a `0..100` scale.
- **Stale subscriptions.** A subscription that goes stale silently stops delivering live
  deltas — the client keeps its last-known state but sees no updates until it reconnects.

## Prior art

- **[featherbear/presonus-studiolive-api](https://github.com/featherbear/presonus-studiolive-api)**
  — unofficial Node/TypeScript library for Series III; the primary reference implementation
  for handshake, state, and control helpers.
- **[samovesel/PreSonus-StudioLive-API](https://github.com/samovesel/PreSonus-StudioLive-API)**
  — dependency-free JavaScript fork.
- **[martinspinler/osclive](https://github.com/martinspinler/osclive)** — Python OSC bridge
  for StudioLive gear.
