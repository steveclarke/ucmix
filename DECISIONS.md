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
