# ucdaw decisions log

Overnight build of `ucdaw` on branch `feat/ucdaw` (from `spike/studio-pro-ucnet-poc`).

## Step 1: set up

- Worktree `~/src/ucmix-worktrees/ucdaw`, branch `feat/ucdaw` from spike head `419388b`.
- Sound: muted briefly at 22:58 NDT (prior state: not muted), then **unmuted at Steve's request** (he is still using the Mac). Sound is left alone for the rest of the run; playback bursts stay short (1–2 s) and always end in Stop.
- Live discovery (22:59 NDT):

```
$ ucdawpoc discover --json
[{"product": "Studio Pro", "version": "8.0.2.110929 macOS Arm64", "deviceClass": "DAW",
  "identity": "<redacted>.studioapp8", "name": "<Mac name>",
  "ip": "<LAN IP>", "controlPort": 62063}]
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
- **InvokeCommand cracked first try** (23:05 NDT):

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

## Security review: resource caps

An automatic review flagged unbounded allocation in `internal/daw/model.go` and `session.go`. Fixed in proportion:

- Frames: already capped by UCNET's 16-bit length (64 KB).
- Decompressed messages (`ZM`, assembled `CK`): capped at 32 MB (`MaxInflated`); larger is rejected with `ErrTooLarge`. The real snapshot is 180 KB.
- `CK` reassembly: a session-side wrapper caps a blob at 16 MB (`MaxBlob`), rejects chunks that run past their declared total, and caps bytes held across chunks that never complete. The shared `proto.ChunkAssembler` is left untouched (mixer code path).
- Model: at most 400,000 distinct paths (`MaxPaths`; the real model has about 156,000); new paths past that are dropped, existing ones still update. The change log was already a bounded ring.
- One test per cap.

## Security review 2: serve socket and discovery trust

- **Socket squatting.** The serve socket now lives in a private per-user directory (`~/Library/Caches/ucdaw/serve.sock` on macOS, `$XDG_RUNTIME_DIR/ucdaw/` on Linux), created 0700; an existing directory must be a real directory owned by the user (mode is tightened if loose). Serve removes a leftover path only if it is a socket owned by the user, and otherwise refuses. Both ends check peer credentials (`LOCAL_PEERCRED` on macOS, `SO_PEERCRED` on Linux): the CLI talks only to a server run by the same user, and the server drops clients of other users. Tests cover each check.
- **Discovery trust.** My first fast path returned on the first Studio Pro announcement, which dropped the July "error on more than one" rule. Restored: discovery keeps listening 1.2 s after the first match (Studio Pro announces about once a second), so two instances still error. Kept: DAW class only, `.studioapp8` identity only, the mixer is never chosen, and the connect address is the announcement's UDP source, never a payload field. New: `--host <ip>` / `UCDAW_HOST` pins Studio Pro to one IP for discovery and the endpoint cache. Tests cover each rule.
- Residual, accepted for a home network: an unpinned LAN host could still announce as Studio Pro; pinning closes that.

## Security review 3: panics, file modes, terminal escapes

- **Panic on LAN input.** `ucdaw discover` indexed `strings.Fields(version)[0]`, which panics on a blank version from a crafted announcement. Now guarded (`discoverLine`). Self-review of every wire read (`parseAnnouncement`, PV/PS/PC/PE/JM/CK decoders, ZM inflate, CK reassembly, capture reader): all length-checked; added fuzz targets `FuzzParseAnnouncement` and `FuzzModelApplyFrame` (ran 15 s and 20 s, no crashes) plus a table test that truncated PV/PS/PC/ZM payloads return errors.
- **File modes.** `ucdaw state record` captures are created 0600 (an existing file is tightened too). The endpoint cache was already 0600 in a 0700 directory; the serve socket is 0600 in a 0700 directory. Hugo capture files set to 0600, directory 0700.
- **Terminal escapes.** Every styled line (messages, list rows, hints, errors, discover lines, serve log) goes through `safe()`, which turns C0/C1 controls, ESC and DEL into `?` (tabs kept in messages, replaced in table cells). `--json` output is left to the JSON encoder, which escapes control characters.

## Steps 2 and 3: schema and core CLI

- `internal/dawschema` expands Studio Pro's own `dawremote.xml` (loaded from the app bundle at runtime, `--schema`/`UCDAW_SCHEMA` to override): 156,443 paths, 399 patterns. Ranged values are converted to and from the 0..1 wire. Fader volume is shown as travel percent because Studio Pro's `fadercurve` is unpublished; dB is not guessed.
- Endpoint cache (`~/Library/Caches/ucdaw/endpoint.json`) makes a direct command ~0.35 s; a cold discovery is ~1.5–3.7 s.
- Live findings:
  - **Stop:** one live Stop was answered by `stop=1, stop=0` with `start` still 1. Stop now waits for `start=0` and presses once more if needed.
  - **Loop:** the loop button carries its target (`PV transport/loop` 1 = on, 0 = off), not a toggle. Studio Pro refuses loop on (echoes 0) while the song has no loop range; ucdaw says so and suggests `Transport > Loop Selection`. A test set a loop end 2 bars in (`Transport > Forward Bar` ×2, `Set Loop End`); Undo did not revert it, so the empty song now has a loop range of 0–2 bars. Loop is off.
- Live run (direct mode, 23:22 NDT):

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
Back to the start of the song.        # did nothing; see "Return to zero" below
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

## Step 6: serve (23:27 NDT)

- `ucdaw serve` holds one subscribed session, sends its own keep-alive every second on the session's connection ID, and reconnects with backoff (1 s doubling to 5 s) when the session ends. CLI commands use it automatically when the socket answers; `--direct` bypasses it.
- Requests are serialized so command change reports do not mix, except Stop, which never waits behind a timed play.
- Live, with serve running: `state` 0.00 s, `click off` 0.04 s, `click on` 0.04 s, `transport play --for 1s` 1.17 s (three runs). One `play --bars 1` took 3.24 s: the first Stop was ignored and the retry stopped it.
- The model stays current from Studio Pro's own updates: tempo set to 100 and a stereo track added over a separate `--direct` connection both showed up in serve's `tempo` and `state` at once (then undone, tempo back to 120).
- **Untested live:** reconnect after Studio Pro restarts, because the brief forbids quitting Studio Pro. The code path is the same `Connect` the CLI uses; the background loop retries after `Done`.

## Step 7: MCP (23:28 NDT)

- `github.com/modelcontextprotocol/go-sdk` v1.8.0 resolved and works. `ucdaw mcp` serves 10 tools on stdio: `transport`, `click`, `tempo`, `get_song`, `get_values`, `get_param`, `set_param`, `list_params`, `list_commands`, `run_command`. Each call uses serve when it runs, direct otherwise. Problems come back as tool errors with "Next: <what to do>".
- Registered at user scope: `claude mcp add --scope user studio-pro-ucnet -- /Users/steve/go/bin/ucdaw mcp` (binary from `go install ./cmd/ucdaw`). The existing `studio-pro` entry is untouched.
- Proof from a fresh `claude -p` (serve running, click off beforehand), transcript in Hugo `personal/projects/talk-to-daw/research/overnight-2026-10-12-mcp-transcript.jsonl`:

```
TOOL click {"state": "on"}                         -> Click on.
TOOL transport {"action": "play", "seconds": 2}    -> Played 2 s at 120 bpm, then stopped.
TOOL tempo {}                                      -> Tempo is 120 bpm.
SAY  The tempo is 120 bpm. The click is on, and the song played for two seconds then stopped.
```

  Whole `claude -p` run 26 s wall (model startup and thinking); the Studio Pro calls themselves take 0.04 s each plus the requested 2 s of playback.

## Commands with arguments, and dialogs

- `InvokeCommand` takes `"commandArgs"` as a JSON object of strings. Live: a new mono track, then
  `{"id": "InvokeCommand","commandCategory": "Track","commandName": "Add Insert to Selected Channels","commandArgs": {"mode": "1","cid": "{D9AE9ACD-69B4-4B43-B8D5-983E39C559A5}","preset": "default"}}`
  put **Pro EQ** on the track (`PS mixer/channel/ch1/inserts/slot1/deviceName "Pro EQ"`). Argument names come from FenderStudioProMCP's macro notes; class IDs from Studio Pro's own `Arm64/Plugins-en.settings`. Guitar Rig 7 is installed and listed there.
- `commandArgs` as an array of `{name, value}` is not understood: Studio Pro opened the command's dialog instead. A modal dialog blocks Undo and other commands until it closes.
- Studio Pro announces dialogs to the remote: `ZM {"id": "BeginDialog","dialogType": 1,"dialogText": "","dialogTitle": "Add Insert to Selected Channels","firstButton": "Cancel","secondButton": "OK"}`, and `JM {"id": "ConfirmDialog","dialogResult": 0}` from the client presses the first button (Cancel), answered by `ZM {"id": "EndDialog"}`. Used live twice to close that dialog on Steve's screen; the test track and insert were then undone (song empty again).
- Decision: ucdaw tracks open dialogs in the model; a command that opens one is cancelled at once and reported, so nothing is left blocking Studio Pro.

## Tracks, plug-ins and presets

- `ucdaw track add [mono|stereo] --name X` runs `Track > Add Audio Track (...)`, waits for the new channel, and renames it with `PS mixer/channel/chN/label` (readback confirmed).
- `ucdaw insert <channel> <plug-in> [--preset P]` selects the channel (`PV .../selected 1`; live, selection is exclusive: the other track went to 0), then runs `Track > Add Insert to Selected Channels` with `commandArgs {mode: "1", cid: <class ID>, preset: P or "default"}`, waits for `inserts/count` to grow, and reads back the slot's `deviceName` and `surface/focusPresetName`.
- Preset argument: the preset's name relative to the plug-in's preset folder, without `.preset`. Live: `"Misc/50 Hz Removal"` loaded (focus preset name `50 Hz Removal`); `"50 Hz Removal"` alone and a full file path both fell back to `default`. ucdaw reports a missing preset plainly.
- Plug-in names and class IDs come from Studio Pro's own `~/Library/Application Support/Fender/Studio Pro 8/Arm64/Plugins-en.settings` (294 effects and instruments on this Mac). `ucdaw plugins [filter]` lists them.
- Inserting opened no plug-in windows (checked the window list).
- Writing `surface/focusPresetName` does not load a preset (no echo).
- Live run with serve:

```
$ ucdaw plugins rig
1 plug-in match "rig".
  Guitar Rig 7  Native Instruments  VST3
$ ucdaw track add mono --name Guitar
Added mono audio track Guitar.
$ ucdaw insert Guitar "Guitar Rig 7"
Added Guitar Rig 7 to Guitar (insert 1).
$ ucdaw insert Guitar "Pro EQ" --preset "Misc/50 Hz Removal"
Added Pro EQ to Guitar (insert 2) with preset 50 Hz Removal.
$ ucdaw insert Guitar "Pro EQ" --preset Sparkle
Added Pro EQ to Guitar (insert 3); Studio Pro has no preset "Sparkle" for it, so it has its default settings.
$ ucdaw state
120 bpm, 4/4, click on, loop off, stopped; 1 channel: Guitar (audio, mono; Guitar Rig 7, Pro EQ, Pro EQ 2).
$ ucdaw command run "Edit > Undo"   (x4)
Ran Edit > Undo: removed Pro EQ 2 from Guitar.
Ran Edit > Undo: removed Pro EQ from Guitar.
Ran Edit > Undo: removed Guitar Rig 7 from Guitar.
Ran Edit > Undo: removed Guitar.
```

- Not testable here: Steve's own Guitar Rig "clean" preset. This Mac has no Studio Pro user presets (`~/Documents/Studio One` holds none). If his clean sound is a Guitar Rig preset saved inside Guitar Rig rather than as a Studio Pro preset, he saves it once from Studio Pro's plug-in window preset menu; then `--preset <name>` loads it.

## Headline flow from a fresh `claude -p`

Prompt: "give me a guitar track with Guitar Rig on it. Then show me the song. Then undo what you did so the song is empty again, and confirm it is." Transcript in Hugo `personal/projects/talk-to-daw/research/overnight-2026-10-12-mcp-guitar-transcript.jsonl`.

```
get_song                        -> 120 bpm, 4/4, click on, loop off, stopped; no tracks yet.
list_plugins "guitar rig"       -> 1 plug-in match "guitar rig".
add_track Guitar, mono          -> Added mono audio track Guitar.
add_insert Guitar, Guitar Rig 7 -> Added Guitar Rig 7 to Guitar (insert 1).
get_song                        -> ... 1 channel: Guitar (audio, mono; Guitar Rig 7).
run_command Edit / Undo         -> Ran Edit > Undo: removed Guitar Rig 7 from Guitar.
run_command Edit / Undo         -> Ran Edit > Undo: removed Guitar.
get_song                        -> ... no tracks yet.
```

## Markers and arranger sections

- `BO` frames starting `EvDL` are event lists: `"EvDL" [len u32] [version u16 = 1] [kind 4] [u32 0] [count u32]`, then per entry `[tag 4] [u32 0] [colour u32] [start f64] [end f64] [name NUL]`, times in seconds. Kinds: `Mark` (tags `MSta`, `Mark`, `MEnd`), `Arng` (arranger sections), `Chrd`, `Lyrc`.
- Decoded live: two markers 2 and 4 bars in (`#2` at 4 s, `#3` at 8 s at 120 bpm), then `Arranger > Create Sections from Markers` gave three sections (0–4, 4–8, 8–300 s). Undone afterwards.
- `ucdaw state` now lists them: `... no tracks yet; sections: Start 0:00, #2 0:12; markers: #2 0:12.` (live, then undone).

## Independent code review

A review agent read the whole branch. Fixed, each with a test where it can be tested without Studio Pro:

- serve dialled Studio Pro while holding its session lock, with no timeout: a Studio Pro that accepted TCP but never answered would hang every request. Now dials outside the lock with a 10 s limit (direct mode 8 s).
- A cancelled button press could leave the button held: the release is now always sent.
- A timed play whose start was not confirmed returned without stopping: it now always sends Stop.
- serve ignored a client hanging up mid timed play: each request now runs under a context cancelled when the client disconnects.
- A lost session was dropped without closing its sockets: now closed.
- Change reports matched channels by path, so deleting one channel misreported the renumbered ones: now matched by name and kind.
- Unchanged values counted as changes; NaN reached Studio Pro; negative play lengths meant endless play; two plug-ins with the same name from different vendors were silently merged; a test race in dawserve. All fixed.
- Not changed: writes in the shared `internal/transport` have no deadline (mixer code path, left alone). Studio Pro selection is exclusive (checked live), so inserting never hits other channels.
- Live: `ucdaw transport play --for 8s` through serve, interrupted with Ctrl-C after 1.5 s: Studio Pro reported `transport/start off` straight after. (The client then printed "Error: context canceled"; it now prints "Interrupted.")
- `golangci-lint run`: 0 issues; `just test`: 698 tests pass.

## Return to zero, and final live checks

- The `transport/returnToZero` button gesture did nothing: after it, Forward Bar ×2 and a marker landed at 0:12 instead of 0:04. `transport rtz` now runs Studio Pro's `Transport > Return to Zero` command. Live check: `transport rtz`, `Forward Bar` ×2, `Marker > Insert` → `markers: #2 0:04`; then Undo. Studio Pro reports no song position, so rtz is sent, not read back.
- `command run` now reports marker and section changes: `Ran Marker > Insert: added marker #2 at 0:02.` / `Ran Edit > Undo: removed marker #2 at 0:02.`
- Dialog auto-cancel, live: `track add --name T`, then `command run "Track > Add Insert to Selected Channels"` with no arguments →
  `Track > Add Insert to Selected Channels opened the "Add Insert to Selected Channels" dialog, which needs choices ucdaw cannot make; canceled it.` No dialog window remained (window list checked); then Undo removed T.
- Two Undos during the night reported no change (one while the dialog was blocking Studio Pro, one after the insert tests). In an empty song they most likely did nothing.
- Final song state: `120 bpm, 4/4, click on, loop off, stopped; no tracks yet.` Loop range 0–2 bars remains from the loop test.

# Round 2 (2026-10-12, from 00:57 NDT)

## Reconnect: skipped on Steve's instruction

- Change from Steve via the coordinator (01:05 NDT): skip step 5 entirely. Studio Pro is not quit, relaunched or restarted tonight, and nothing is done that could disconnect it. Reconnect stays as written in round 1 and is listed in the hand-back note as "to test with Steve".

## Step 1: arrangement edits (01:05–01:40 NDT)

Route 1 (Studio Pro's own commands with `commandArgs`) worked; routes 2–4 (InvokeMethod, macros) were not needed.

- **Where the argument names came from.** The macros in `~/Documents/Studio One/Macros` (Studio Pro's factory macros) carry `CommandArgument` pairs: `Arranger / Goto Section {Number, Name}`, `Transport / Forward Bar {Bars}`, `Marker / Insert Named {Name}`. The binary has `Number,Name,Play` next to Goto Section. Studio Pro's script packages (`Contents/Scripts/*.package`, `PACKAGEF` header then zlib streams) hold the edit tasks in JavaScript: `Rename Events.js` reads `context.parameters.addString ("New Name")`, so `Event / Rename Events {"New Name": …}` renames without a dialog.
- **Goto Section by Number selects the section.** `Goto Section {Number: n}` (1-based) moves the cursor to section n *and selects it on the arranger track*. `{Name: "Chorus"}` alone also worked; `{Number: "", Name: …}` (as the factory macros write it) did nothing over the remote. ucdaw always selects by number, because names repeat ("Chorus", "Chorus").
- **What each edit is, proven live against the arranger list (`BO EvDL Arng`):**
  - duplicate: Goto Section n, `Edit / Duplicate` → the copy goes right after, later sections and markers shift.
  - repeat X after Y: Goto X, `Edit / Copy`, cursor to Y's end (Goto the next section, or Return to Zero + `Forward Bar {Bars}` when Y is last), `Edit / Paste` → inserted at the cursor, later sections shift.
  - delete: Goto n, `Arranger / Delete Range` → section gone, gap closed. (`Edit / Delete` removes the section but leaves the gap.)
  - move: copy + paste at the target, then Delete Range on the original. Two undo steps; the answer says so.
  - rename: Goto n, `Event / Rename Events {"New Name": name}`. Without arguments it opens the Rename dialog (auto-cancelled); with a wrong argument name it renames to "" (seen live with `Name`, `Text`, `Title`…, each undone).
  - add: Return to Zero + Forward Bar to the end of the last section, `Edit / Create Range from Cursor`, Forward Bar {bars}, `Edit / Range End to Cursor`, `Arranger / Add Arranger Section from Selection`; Studio Pro auto-names it ("Intro", "Verse"), then ucdaw renames it. Works in a song with no sections at all.
- Live run (serve, 01:35 NDT):

```
$ ucdaw section
1 section: Intro bar 1 (8 bars).
$ ucdaw section rename Intro Verse
Renamed Intro to Verse.
$ ucdaw section add Chorus Bridge
Added sections Chorus and Bridge, 8 bars each; sections now: Verse bar 1, Chorus bar 9, Bridge bar 17.
$ ucdaw section repeat Chorus after Bridge
Repeated Chorus after Bridge, at bar 25.
$ ucdaw section duplicate Verse
Duplicated Verse; the copy starts at bar 9 and the rest moved 8 bars later.
$ ucdaw section move Bridge before "Chorus 1"
Moved Bridge before Chorus; it now starts at bar 17 (undo takes two steps).
$ ucdaw section delete "verse two"
Deleted Verse (bar 9, 8 bars); the rest moved up to close the gap.
$ ucdaw section repeat chorus two after the bridge
Repeated Chorus after Bridge, at bar 17; the rest moved 8 bars later.
```

- Side effects worth knowing: Copy/Paste uses Studio Pro's clipboard. `Delete Range` leaves song markers that sat inside the section at its start instead of deleting them (sections are right; markers are Studio Pro's to keep).
- Selection is not reported by the remote, so ucdaw cannot check the selection before Duplicate/Delete; it checks the arranger list after and says plainly when it does not match, with the undo command.
- `command run` gained `--arg name=value` and MCP `run_command` an `args` object, since arguments turned out to be the key to most commands.
- Blocked by the auto-mode classifier (not retried): `Edit / Select All` + `Edit / Delete` to clear the test arrangement. The blank-arrangement case was reached with Undo instead.
