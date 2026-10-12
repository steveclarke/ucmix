# ucdaw decisions log

Overnight build of `ucdaw` on branch `feat/ucdaw` (from `spike/studio-pro-ucnet-poc`).

## Step 1: set up

- Worktree `~/src/ucmix-worktrees/ucdaw`, branch `feat/ucdaw` from spike head `419388b`.
- Sound: muted briefly at 22:58 NDT (prior state: not muted), then **unmuted at Steve's request** (he is still using the Mac). Sound is left alone for the rest of the run; playback bursts stay short (1–2 s) and always end in Stop.
- Live discovery (22:59 NDT):

```
$ ucdawpoc discover --json
[{"product": "Studio Pro", "version": "8.0.2.110929 macOS Arm64", "deviceClass": "DAW",
  "identity": "<redacted>.studioapp8", "name": "Steve's MacBook Air",
  "ip": "192.168.77.78", "controlPort": 62063}]
```

## Early protocol findings (before step 2, because they shape the design)

- **Snapshot decoded.** After `SubscriptionReply`'s predecessors, Studio Pro sends:
  - `CK` chunks tagged `\0\0ZM`; the joined chunk data is a raw zlib stream holding
    `{"id": "AdviseCommands","categories": [...]}`: 52 categories, 1449 commands,
    each `{displayName, name, category, englishName}`. The command catalog comes from Studio Pro itself.
  - `ZM` frames: 4-byte LE uncompressed length + zlib. One is `UpdateTimeContext`
    (sample rate, tempo map, time signature map), one is `Synchronize`
    (`values` / `children` / `states` / `ranges` tree of the live song: transport, metronome, mixer channels, inserts).
  - `TC` (`LpSt`, `LpEn`: loop start/end), `BO` (`EvDL` event lists: markers, arranger, chords, lyrics), then `JM SubscriptionReply`.
- **Values on the wire are normalized 0..1** for ranged params (tempo 0.282 = 120 bpm on 10..400; counts are n/1000).
- **InvokeCommand cracked first try** (23:20 NDT):

```
>>> JM {"id": "InvokeCommand","commandCategory": "Transport","commandName": "Click"}
  1480 PV metronome/clickOn=0
>>> (same again)
  1422 PV metronome/clickOn=1          # restored
>>> JM {"id": "InvokeCommand","commandCategory": "Track","commandName": "Add Audio Track (mono)"}
       PV mixer/channel/count=0.001 ; PS mixer/channel/ch1/label "Track 1" ; PS .../type "AudioTrack" ...
>>> JM {"id": "InvokeCommand","commandCategory": "Edit","commandName": "Undo"}
       PS mixer/channel/ch1/* cleared ; PV mixer/channel/count=0   # song back to empty
```

- New-channel updates arrive as `PV` (float), `PS` (key, NUL, 2 bytes, string, NUL) and `PC` (key, NUL, 2 bytes, uint32 colour) frames, so the live model can be kept current from ordinary parameter frames.

## Security review: resource caps (23:20 NDT)

An automatic review flagged unbounded allocation in `internal/daw/model.go` and `session.go`. Fixed in proportion:

- Frames: already capped by UCNET's 16-bit length (64 KB).
- Decompressed messages (`ZM`, assembled `CK`): capped at 32 MB (`MaxInflated`); larger is rejected with `ErrTooLarge`. The real snapshot is 180 KB.
- `CK` reassembly: a session-side wrapper caps a blob at 16 MB (`MaxBlob`), rejects chunks that run past their declared total, and caps bytes held across chunks that never complete. The shared `proto.ChunkAssembler` is left untouched (mixer code path).
- Model: at most 400,000 distinct paths (`MaxPaths`; the real model has about 156,000); new paths past that are dropped, existing ones still update. The change log was already a bounded ring.
- One test per cap.

## Security review 2: serve socket and discovery trust (23:50 NDT)

- **Socket squatting.** The serve socket now lives in a private per-user directory (`~/Library/Caches/ucdaw/serve.sock` on macOS, `$XDG_RUNTIME_DIR/ucdaw/` on Linux), created 0700; an existing directory must be a real directory owned by the user (mode is tightened if loose). Serve removes a leftover path only if it is a socket owned by the user, and otherwise refuses. Both ends check peer credentials (`LOCAL_PEERCRED` on macOS, `SO_PEERCRED` on Linux): the CLI talks only to a server run by the same user, and the server drops clients of other users. Tests cover each check.
- **Discovery trust.** My first fast path returned on the first Studio Pro announcement, which dropped the July "error on more than one" rule. Restored: discovery keeps listening 1.2 s after the first match (Studio Pro announces about once a second), so two instances still error. Kept: DAW class only, `.studioapp8` identity only, the mixer is never chosen, and the connect address is the announcement's UDP source, never a payload field. New: `--host <ip>` / `UCDAW_HOST` pins Studio Pro to one IP for discovery and the endpoint cache. Tests cover each rule.
- Residual, accepted for a home network: an unpinned LAN host could still announce as Studio Pro; pinning closes that.

## Security review 3: panics, file modes, terminal escapes (00:05 NDT)

- **Panic on LAN input.** `ucdaw discover` indexed `strings.Fields(version)[0]`, which panics on a blank version from a crafted announcement. Now guarded (`discoverLine`). Self-review of every wire read (`parseAnnouncement`, PV/PS/PC/PE/JM/CK decoders, ZM inflate, CK reassembly, capture reader): all length-checked; added fuzz targets `FuzzParseAnnouncement` and `FuzzModelApplyFrame` (ran 15 s and 20 s, no crashes) plus a table test that truncated PV/PS/PC/ZM payloads return errors.
- **File modes.** `ucdaw state record` captures are created 0600 (an existing file is tightened too). The endpoint cache was already 0600 in a 0700 directory; the serve socket is 0600 in a 0700 directory. Hugo capture files set to 0600, directory 0700.
- **Terminal escapes.** Every styled line (messages, list rows, hints, errors, discover lines, serve log) goes through `safe()`, which turns C0/C1 controls, ESC and DEL into `?` (tabs kept in messages, replaced in table cells). `--json` output is left to the JSON encoder, which escapes control characters.

## Steps 2 and 3: schema and core CLI (00:20 NDT)

- `internal/dawschema` expands Studio Pro's own `dawremote.xml` (loaded from the app bundle at runtime, `--schema`/`UCDAW_SCHEMA` to override): 156,443 paths, 399 patterns. Ranged values are converted to and from the 0..1 wire. Fader volume is shown as travel percent because Studio Pro's `fadercurve` is unpublished; dB is not guessed.
- Endpoint cache (`~/Library/Caches/ucdaw/endpoint.json`) makes a direct command ~0.35 s; a cold discovery is ~1.5–3.7 s.
- Live findings:
  - **Stop:** one live Stop was answered by `stop=1, stop=0` with `start` still 1. Stop now waits for `start=0` and presses once more if needed.
  - **Loop:** the loop button carries its target (`PV transport/loop` 1 = on, 0 = off), not a toggle. Studio Pro refuses loop on (echoes 0) while the song has no loop range; ucdaw says so and suggests `Transport > Loop Selection`. A test set a loop end 2 bars in (`Transport > Forward Bar` ×2, `Set Loop End`); Undo did not revert it, so the empty song now has a loop range of 0–2 bars. Loop is off.
- Live run (direct mode, 00:18 NDT):

```
$ ucdaw state
120 bpm, 4/4, click on, loop off, stopped; no tracks yet.
$ ucdaw click off / click on / click on
Click off. / Click on. / Click is already on.
$ ucdaw tempo 96 / tempo 120
Tempo 96 bpm (was 120). / Tempo 120 bpm (was 96).
$ ucdaw transport loop on
Loop on.
$ ucdaw transport play --for 1s
Played 1 s at 120 bpm, then stopped.
$ ucdaw transport play --bars 1
Played 1 bar at 120 bpm, then stopped.
$ ucdaw transport rtz
Back to the start of the song.
$ ucdaw command run Track "Add Audio Track (mono)"
Ran Track > Add Audio Track (mono): added Track 1 (audio).
$ ucdaw param set mixer/channel/ch1/label Guitar
Guitar label: "Guitar" (was "Track 1").
$ ucdaw param set mixer/channel/ch1/volume 50
Guitar volume: 50% (was 76%).
$ ucdaw command run "Edit > Undo"   (x2)
Ran Edit > Undo: Guitar volume: 76%.
Ran Edit > Undo: removed Guitar.
$ ucdaw state
120 bpm, 4/4, click on, loop on, stopped; no tracks yet.
$ ucdaw transport loop off
Loop off.
$ ucdaw params tempo
1 parameter match "tempo"; <n> is a channel or slot number.
  transport/tempo  float  10..400 bpm.value
$ ucdaw command list "zoom full"
4 Studio Pro commands match "zoom full".
```
