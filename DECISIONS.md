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

- Change from Steve via the coordinator (about 01:00 NDT): skip step 5 entirely. Studio Pro is not quit, relaunched or restarted tonight, and nothing is done that could disconnect it. Reconnect stays as written in round 1 and is listed in the hand-back note as "to test with Steve".

## Step 1: arrangement edits

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
- Live run (serve):

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

## Step 2: audio events and edits

- **Audio without a microphone or a file dialog.** Recording would capture Steve's room and Import File risks a native file panel ucdaw cannot cancel, so the test take was made inside Studio Pro: `Track / Add Instrument and Track {mode: 1, cid: <Mojito>, preset: default, openEditor: 0}` (no editor window opened), `Instrument Parts / Insert Instrument Part` (one bar at the cursor, from `Insert Part.js`), `Musical Functions / Fill with Notes {…}` (arguments from the factory macros), `Event / Bounce Selection {Snap: 0}` → a new stereo audio track with the rendered take. Renamed it "Take", duplicated the event to four bars and bounced again into one 4-bar event.
- **Studio Pro does not send audio events to remotes.** A capture (`state record`) during the bounce shows only the new channel's `PV`/`PS`/`PC` values and the focus change: no `BO` kind, no `ZM` message for events. Event lists are only markers, arranger, chords and lyrics. So audio edits are sent and answered "not read back"; ucdaw says so in every answer rather than claiming a confirmation it does not have.
- **Selecting a track's events:** `PV mixer/channel/chN/selected 1` selects the track (exclusive), then `Edit / Select All on Tracks` selects its events. Every edit below then acts on that track only (checked on screen: the Mojito track was untouched).
- **Edits, each checked on screen (window captures of Studio Pro only, kept out of the repo):**
  - fade out the last bar: `Transport / Locate Selection End`, `Transport / Rewind Bar {Bars: 1}`, `Audio / Fade Out from Cursor`.
  - fade in: `Transport / Locate Selection`, `Forward Bar {Bars}`, `Audio / Fade In to Cursor`.
  - split at bar N: `Return to Zero`, `Forward Bar {Bars: N-1}`, `Edit / Split at Cursor`.
  - trim: same cursor, `Event / Trim End to Cursor` / `Trim Start to Cursor`.
  - crossfade: `Audio / Create Crossfades` only joins events that overlap (its script checks `left.end > right.start`); a plain split has no overlap, so ucdaw also runs `Audio / Create Autofades {Length: seconds}` (0.25 gave ~0.25 s fades on every edge).
  - quantize: `Event / Quantize`.
- "Crossfade the end of the guitar take" reads as a fade-out of the end; the MCP description says so.
- **Arrangement edits carry the audio.** `section duplicate Verse` copied the Mojito part and the take into the new Verse at bar 9 (checked on screen), then undone.
- Live run (serve):

```
$ ucdaw audio fade-out Take            (run earlier as raw commands)
$ ucdaw audio fade-in Take
Faded in the first bar of Take. Studio Pro does not report audio events to remotes, so this one is not read back.
$ ucdaw audio split Take 2
Split Take at bar 2. Studio Pro does not report audio events to remotes, so this one is not read back.
$ ucdaw audio trim-end Take 4
Trimmed the end of Take to bar 4. …
$ ucdaw audio crossfade Take --ms 50
Crossfaded the joins in Take, with 50 ms fades on every edit. …
$ ucdaw audio quantize Take
Quantized Take to the quantize grid. …
```

## Step 3: favorites and instrument tracks

- **Instrument tracks.** Studio Pro's `audioedit.package` script (`Insert FX.js`) has `Track / Add Instrument and Track`: it reads `mode`, `cid`, `preset` and `openEditor` from its arguments, makes a new instrument track, loads the instrument and connects it. `openEditor: "0"` keeps the instrument window closed (window list checked after every add). Live: Mojito, Syntronik 2 and MODO BASS 2 each added in 0.4–1.5 s.
- The remote shows an instrument track as its instrument's channel (`type AudioSynth`, label = instrument name). Renaming that channel (`PS label`) renames the track too (seen on screen: track header "Pad").
- **Instrument preset readback.** Studio Pro reports `surface/focusPresetName` only for the device in focus. `Console / Show Instrument Editor` toggles the selected instrument's window: ucdaw opens it, reads the preset, and closes it again (a moment on screen), and only when a preset was asked for and no other editor is in focus. Live: Mojito with preset `Transformator` read back as "Transformator"; with `LuvErasor` → "(preset LuvErasor)". VSTs that report no preset name fall back to checking the preset file.
- **Preset files.** Studio Pro presets live in `~/Documents/Studio One/Presets/<vendor>/<plug-in>/` (user; empty on this Mac) and `Studio Pro 8.app/Contents/Presets/<vendor>/<plug-in>/` (factory). `favorite set` warns when the named preset file does not exist yet, so Steve knows to save it from the plug-in window first.
- **Favorites** (`internal/dawfav`): `~/Library/Application Support/ucdaw/favorites.json`, 0600 in a 0700 directory, written atomically, at most 500 entries and 1 MB, control characters stripped. Matching ignores case, punctuation and the words "my", "the", "preset", "sound", "usual", so "my clean preset" finds "clean", and a unique partial match ("clean" → "clean guitar") works. `insert`, `track add --insert` and `instrument add` resolve favorites; an explicit `--preset` overrides the favorite's. Spelling is US ("favorite") because the repo's linter enforces it.
- Live run (serve; the test favorites were removed afterwards, so Steve's file is empty):

```
$ ucdaw favorite set "pad" Mojito --preset LuvErasor
Remembered pad: Mojito with preset LuvErasor (instrument).
$ ucdaw favorite set "clean guitar" "Guitar Rig 7" --preset Clean
Remembered clean guitar: Guitar Rig 7 with preset Clean; Studio Pro has no preset file "Clean" for Guitar Rig 7 yet, so save it from the plug-in window's preset menu.
$ ucdaw instrument add "my pad" --name Pad2
Added instrument track Pad2 with Mojito (preset LuvErasor), from your favourite "pad".
$ ucdaw instrument add "worship synth" --name Keys
Added instrument track Keys with Syntronik 2, from your favourite "worship synth".
$ ucdaw instrument add bass --name Bass
Added instrument track Bass with MODO BASS 2, from your favourite "bass".
$ ucdaw track add --name Gtr --insert "Pro EQ" --preset "Misc/50 Hz Removal"
Added mono audio track Gtr; added Pro EQ to Gtr (insert 1) with preset 50 Hz Removal.
$ ucdaw insert Gtr "my clean guitar"
Added Guitar Rig 7 to Gtr (insert 2); Studio Pro has no preset "Clean" for it, so it has its default settings, from your favourite "clean guitar".
```

  (Messages then said "favourite"; now "favorite".) All undone afterwards.
- **Known glitch (round 1 code):** Undo reports while removing several renamed channels in a row named the wrong channel twice ("removed Guitar Rig 7 from X" for Gtr). The song itself was right. Cause: channels renumber as one is removed and the label updates arrive across the settle window. Logged, not fixed tonight.
- An arranger duplicate with content once needed two Undo presses: the first reported no change, the second reverted the sections and content.

## Step 4: faders in dB (done by 01:28 NDT)

- `fadercurve` is not in any file Studio Pro ships (it lives in the binaries), so it was measured: the console (`View / Console`) shows each fader's level as text, and ucdaw set `mixer/channel/ch2/volume` to 36 wire positions and read Studio Pro's text from a capture of its window after each.
- Result: straight lines in dB between (0.04, −60), (0.16, −40), (0.52, −10), (0.76, 0) and (1.0, +10); below 0.04 the same line continues at 2100 dB per unit (−139.8 at 0.002), and 0 is −inf. Every measured point matches to within 0.05 dB (the console's rounding). +10 dB = gain 3.1622, the top of the published range, which confirms the scale.

```
wire  1    .95  .9   .85  .8   .76 .7   .65  .6   .55  .53  .52  .51   .5    .45   .4  .35   .3    .25   .2    .17   .16 .15   .1  .05   .045  .04 .035  .03 .025  .02  .015   .01  .005  .002  0
dB   +10  +7.9 +5.8 +3.7 +1.7  0  -2.5 -4.6 -6.7 -8.8 -9.6 -10 -10.8 -11.7 -15.8 -20 -24.2 -28.3 -32.5 -36.7 -39.2 -40 -41.7 -50 -58.3 -59.2 -60 -70.5 -81 -91.5 -102 -112.5 -123 -133.5 -139.8 -oo
```

- Shipped: every `fadercurve` parameter (channel volume, send and cue-mix levels) now reads and takes dB (`param set …/volume -6`, "60%" still sets travel); `ucdaw fader <track> [dB] [--up N|--down N]`; MCP `fader` (`change: -3` for "bring the pad down 3 dB"); `get_song` lists a track's level when it is not 0 dB, and "muted".
- `fader` reads its own arguments because cobra takes "-6" for a flag.
- Live check against the console, ucdaw's answer then the console's text:

```
$ ucdaw fader Take -6          Take at -6 dB (was 0 dB).        console: -6.0
$ ucdaw fader Take --down 3    Take at -9 dB (was -6 dB).       console: -9.0
$ ucdaw fader Take --up 1.5    Take at -7.5 dB (was -9 dB).     console: -7.5
$ ucdaw fader Take -33.3       Take at -33.3 dB (was -7.5 dB).  console: -33.3
$ ucdaw fader Take +4.2        Take at +4.2 dB (was -33.3 dB).  console: +4.2
$ ucdaw fader Take -inf        Take at -inf dB (was +4.2 dB).   console: -oo
$ ucdaw fader Take -75         Take at -75 dB (was -inf dB).    console: -75.0
```

  Fader left at 0 dB; the console view was closed again.

## Step 6: the full scene from a fresh `claude -p`

- Prompt (the Brief's Saturday morning): "Set me up for a worship demo at 72 BPM with the click on. Give me a guitar track with Guitar Rig and a clean preset, and three backing tracks: pad, keys, bass. Then add sections Verse, Chorus, Bridge and repeat Chorus after the Bridge."
- Run with only this MCP (`--strict-mcp-config`, `studio-pro-ucnet` = the installed `ucdaw mcp`), serve running. Transcripts in Hugo `personal/projects/talk-to-daw/research/round2-2026-10-12-scene-transcript.jsonl` (run 1) and `…-transcript-2.jsonl` (run 2).
- **Run 1** (32 s): everything happened and was read back; tempo 72, Guitar with Guitar Rig 7, Pad (Syntronik 2), Keys (SampleTank 4), Bass (MODO BASS 2), sections Verse 1, Chorus 9, Bridge 17, Chorus 25. What read badly:
  - `get_song` listed sections as clock times ("Chorus 0:27") at 72 bpm, while `section` spoke in bars. Fixed: the song summary now lists sections and markers by bar.
  - "Studio Pro has no preset "Clean" for it" left Claude guessing how to make one. Fixed: missing-preset answers say what presets exist, or that none are saved and how to save one; new `list_presets` tool and `ucdaw presets`.
- Run 1 undone with Undo (13 steps; tempo is not in Studio Pro's undo history, so it was set back to 120 by hand).
- **Run 2** (60 s, 20 turns): Claude checked Guitar Rig's presets first (`list_presets` → none saved), built everything in parallel, and answered in Steve's shape:

```
TOOL tempo {"bpm": 72}                                           -> Tempo 72 bpm (was 120).
TOOL click {"state": "on"}                                       -> Click is already on.
TOOL add_track {"name": "Guitar", "plugin": "Guitar Rig 7"}      -> Added mono audio track Guitar; added Guitar Rig 7 to Guitar (insert 1).
TOOL add_instrument {"instrument": "Syntronik 2", "name": "Pad"} -> Added instrument track Pad with Syntronik 2.
TOOL add_instrument {"instrument": "SampleTank 4", "name": "Keys"} -> Added instrument track Keys with SampleTank 4.
TOOL add_instrument {"instrument": "MODO BASS 2", "name": "Bass"} -> Added instrument track Bass with MODO BASS 2.
TOOL section {"action": "add", "name": "Verse, Chorus, Bridge"}  -> Added sections Verse, Chorus and Bridge, 8 bars each; sections now: Verse bar 1, Chorus bar 9, Bridge bar 17.
TOOL section {"action": "repeat", "section": "Chorus 1", "target": "Bridge"} -> Repeated Chorus after Bridge, at bar 25.
SAY  Everything is set up except the clean guitar sound. Guitar Rig 7 is on the Guitar track but on its default sound, because no clean preset is saved in Studio Pro. To fix it, pick a clean sound in the Guitar Rig window, then save it from Studio Pro's preset menu at the top of that window. …
```

- Run 2's result is left in the throwaway song for Steve to see. The "Mojito" instrument and a "Bridge" marker at bar 1 in that song are leftovers of the night's tests.
- After the scene, `fader Pad --down 3` then `Edit / Undo`: Undo reported no change and the fader stayed at −3 dB (round 1 saw a fader change undone). Some Undo presses act on steps the remote cannot see (selection, arm), so "undo that" after a fader move is not reliable; setting the old level back is. Open question, logged.

## Hand-back checks

- `section play "Chorus 1" --for 1` live: "From Chorus: Played 1 s at 72 bpm, then stopped." Plain "Chorus" with two choruses answers "The song has 2 "Chorus" sections. say which: Chorus 1 (bar 9) or Chorus 2 (bar 25)".
- Files Studio Pro wrote because of the tests: `Bounces/Track 1.wav` and `Bounces/Take.wav` (plus a peak cache file) in the throwaway song's folder, from the two `Event / Bounce Selection` runs; listed in the Hugo round 2 note. Studio Pro's own autosaves (`History/`) also continued as usual.
- `just test`: 739 tests pass; `golangci-lint`: 0 issues on the ucdaw packages.

# Importing stems (2026-10-12, from 08:50 NDT)

## What carries the import

- `Session / Import File` ignores arguments and opens the macOS file panel (proven earlier today); not used.
- `open -a "Studio Pro 8" <file>` imports onto the selected track at the playhead (proven earlier today). ucdaw runs it as `exec.Command("open", "-g", "-a", "/Applications/Studio Pro 8.app", <absolute cleaned path>)`: no shell, and the path is absolute, so it can never read as a flag. Live: with `-g` Chrome stayed the front app through an import (`lsappinfo front` before and after), so Studio Pro does not jump in front of Steve on every stem.
- Recipe per stem: `Track / Add Audio Track (mono|stereo)` to match the file (read from its WAV or AIFF header), rename it, confirm `selected` (`PV .../selected 1`, read back), `Transport / Return to Zero` (+ `Forward Bar {Bars}` for `--at`), `open`, wait for the landing signal.

## The landing signal

- **UCNET carries nothing for an import.** Three captures (`ucdaw state record`) around imports: after the new track's values, the only frames were `ZM {"id": "BeginProgress"}` / `ZM {"id": "EndProgress"}`. The third capture ran Return to Zero, waited 3 s, then imported: the pair arrived right after Return to Zero, three seconds before the import, and nothing came with the import itself. The pair belongs to Return to Zero.

```
cap3 (ms since subscribe): 4227 ZM BeginProgress / 4227 ZM EndProgress   <- rtz at ~4.0 s
                           (open -a at ~7.1 s: no frame)                  12811 PS document/title (periodic)
```

- **The song's waveform cache is the signal.** Studio Pro writes `<song>/Cache/Images/<hash><file name><frames in upper-case hex>.peak` when it reads a new file: `x270F117B01_Kick_Final.wav2EE00.peak` (0x2EE00 = 192000 frames = 4 s at 48 kHz), `x270F117B03-Pad (Stereo) v2.wav56220.peak` (352800 = 8 s at 44.1 kHz: frames at the file's own rate). The hash changes with the folder. ucdaw finds the song folder from `document/title` (`~/Documents/Studio One/Songs/<title>/`, checked for `<title>.song` or `.song.autosave`) and watches for a new file ending in `<name><frames hex>.peak`.
- A file the song has already read keeps its old waveform file (re-importing the Pad: mtime unchanged), yet it did import (the undo sequence proved both clips). So when the waveform file already exists, or the song folder is not found, ucdaw waits a fixed 2 s instead and the answer says the waveform was drawn for only some files.
- Measured: the waveform file appeared 0.2–0.45 s after `open` for 0.7–1.2 MB stems. Window captures every ~300 ms showed the clip on its track in the frame after the waveform file's mtime. ucdaw waits 0.3 s more after the waveform before the next track is added. The wait for a waveform is 15 s plus 1 s per 20 MB of audio, at most 3 minutes; a stem that never lands stops the batch with what was done so far.

## Clip names

- A clip is named after its file ("02 Bass DI"). `Event / Rename Events {"New Name": "Bass DI"}` renamed it (checked on screen). Sent 0.3 s after the waveform appeared, the rename was ignored twice (both AIFF files; screenshots every 300 ms showed the clip in place but never renamed); the same commands sent a few seconds later worked. So ucdaw renames all clips at the end of a batch, 1.5 s after the last import, each after `Edit / Select All on Tracks` on its own new track, so the rename cannot touch anything else. With `--onto` clips keep their file names.
- Undo, counted live: about three presses per stem (a batch of four took 12; a batch of three took 7, the track removed on every second or third press). The safe instruction is to press Undo until `get_song` no longer lists the tracks.

## Formats and dialogs

- The song is 44.1 kHz (`UpdateTimeContext` `sampleRate: 44100`). 48 kHz WAV, 44.1 kHz WAV and 48 kHz 24-bit AIFF all imported with no dialog; Studio Pro writes a sample-rate cache under `Cache/Audio` a moment later. So the dialog path is **untested live**: no conversion question appeared. ucdaw accepts a two-button dialog whose title or text mentions converting or the sample rate (presses the button labelled Convert/OK/Yes/Import, never Cancel) and cancels anything else, reporting it. `Session.AnswerDialog(ctx, 0|1)` sends `{"id": "ConfirmDialog","dialogResult": n}`; `DismissDialog` is now `AnswerDialog(ctx, 0)`.
- Headers: WAV (RIFF, RF64 via `ds64`, BWF with `bext` before `fmt `, `WAVE_FORMAT_EXTENSIBLE`) and AIFF/AIFC (`COMM`, 80-bit sample rate). At most 256 chunk headers and 4 KB of a format chunk are read; audio data is never read; fuzzed (`FuzzParseAudioHeader`, 380k runs, no panic). MP3, FLAC, M4A, OGG, CAF are skipped with "convert it first". More than two channels is skipped. At most 64 files per import.
- Names: leading track numbers (1–2 digits, or more with a leading zero: "808 Sub" keeps its 808), version tags (v2), bit depths, sample rates, tempos, dates and the words final, stem, bounce, print, master, stereo, mono… are dropped; brackets are opened, not deleted ("Snare (Mic 2)" → "Snare Mic 2"); a hyphen between letters stays ("Hi-Hat"); a prefix every file shares is dropped ("Amazing Grace_Kick" → "Kick"); repeats get 2, 3. Control characters are stripped.
- Same Mac only: `open` acts on this Mac, so ucdaw refuses when Studio Pro's address is not one of this Mac's.
- The CLI resolves paths (and `~`) before the request crosses to serve, whose working folder differs. The CLI gives an import 30 minutes instead of the usual 90 s.

## Live test

Stems generated with ffmpeg under `~/Music/ucdaw-test/stems/`: `01_Kick_Final.wav` (mono 48 kHz 16-bit, 4.5 s), `02 Bass DI.wav` (stereo 48 kHz, 6.5 s), `03-Pad (Stereo) v2.wav` (stereo 44.1 kHz, 8.5 s), `04_Lead_Vox_24bit.aif` (mono 48 kHz 24-bit AIFF, 5.5 s), plus `notes.txt`. Through serve:

```
$ ucdaw import ~/Music/ucdaw-test/stems            (2.6 s wall)
Imported 4 stems onto new tracks at bar 1: Kick (mono), Bass DI (stereo), Pad (stereo) and Lead Vox (mono). Skipped notes.txt (not audio). Tracks read back; Studio Pro does not report clips to remotes, so placement is not read back (Studio Pro drew each file's waveform, so it read every file).
$ ucdaw state
… 11 channels: …, Kick (audio, mono), Bass DI (audio, stereo), Pad (audio, stereo), Lead Vox (audio, mono); …
```

- Window capture after the run: four tracks Kick, Bass DI, Pad, Lead Vox, each clip starting on the bar-1 line and named like its track. Left in the song for Steve.
- My mistake during the run: a stray second `ucdaw import … &` in the same shell line was not stopped by `kill %1` and imported the four stems again onto four more tracks. Undone with 12 Undo presses (three per stem, as above), channel count read back 15 → 11.
- Kick's clip on screen is 4 s long though the regenerated file is 4.5 s. The stems were regenerated in place (same names) after the development imports of the 4 s versions; Studio Pro seems to keep a file's length from earlier in the session. Real stems are not rewritten under the same name mid-session; noted, not chased.
- `just test`: all pass; `golangci-lint`: 0 issues on the ucdaw packages.

## Hardening and follow-up checks (09:15–09:30 NDT)

- **Header parsing, after an automated security review flagged a denial-of-service risk.** The walk already used seeks (no audio read) and at most 256 chunk headers. Now also: each chunk body is read through `io.LimitReader` capped at 4 KB, a chunk that claims to run past the end of the file is malformed (except the audio itself, `data`/`SSND`, which a file still being written may cut short), every step must move the offset forward, and a walk that ends without the format chunk is an error. New test `TestReadAudioFileBoundsHostileHeaders`: oversized `fmt `, a chunk past the end, 266 zero-length chunks, truncated headers, an `RF64` with a huge `ds64`; each fails in well under a second. Fuzzed again 15 s, no panic.
- **A 52 MB stem** (3 minutes, stereo 48 kHz 24-bit) with a small one after it: the waveform file appeared 0.26 s after `open` and was written whole (its size never changed across 100 polls 50 ms apart); the small stem landed on its own track. Checked on screen.
- **Clip names went to the wrong clip in that run:** the small stem's clip was named "Big Organ". Studio Pro applies a track selection (`PV selected`) at once but runs commands a moment later, so the next track's selection overtook the previous rename. Fixed: each rename is Deselect All, Select All on Tracks, Rename, then a 0.5 s pause before the next track is selected. Re-run with three stems (52 MB WAV, small WAV, AIFF): every clip named after its own track, on screen.
- **`--onto` and `--at`, live:** `ucdaw import "Small Shaker.wav" --onto "Probe a" --at 3` → `Imported Small Shaker.wav onto Probe a at bar 3. …`; on screen the clip starts on the bar-3 line of Probe a and keeps its file name. One Undo removes it.
- All these test tracks were undone (channel count read back 14 → 11); the four stems from the main test stay.
- Playback was running when I took the last window capture (position 1.3.2, about 2 s in); `ucdaw` had not sent Play (the commands just before were eight Undos, a parameter read, Return to Zero and `state`, which reported stopped). Stopped it at once; `transport/start: off` read back. Return to Zero alone, tried again, did not start playback. Cause unknown; possibly Steve at the screen.

# Starting a new song (2026-10-12, from 09:30 NDT)

## What carries it

- `InvokeCommand File / New Session` opens Studio Pro's New window and announces it as `BeginDialog` titled "New", buttons **OK, Cancel** (OK first; the Add Insert dialog lists Cancel first). `ConfirmDialog` with OK's index creates the song with the window's current template and settings; `document/title` changes to the new name. The session and serve follow the new song with no reconnect; the previous song stays open in its own tab.
- `ucdaw song new [--tempo BPM] [--click on|off]` and MCP `new_song` (`tempo`, `click`): refuse if a dialog is already open; Invoke; wait up to 5 s for a dialog; press the button labeled OK only if the dialog is titled "New"; wait up to 20 s for `document/title` to change; let the new song's own values settle (otherwise its default tempo can land on top of the one ucdaw sets); then set tempo and click through the usual read-back paths and report `st.Song()`.
- An unconfirmed `EndDialog` after OK is not a failure (a song can take more than AnswerDialog's 2 s to load); the title change is the proof. No title change in 20 s is reported plainly, and tempo and click are then left alone, so they never land on the old song.
- A different dialog (title not "New", or no OK button) is cancelled and reported; nothing else is pressed.

## Auto-cancel and the New window

- Every other command cancels a dialog it opens (`run`, track, insert, instrument, audio, import). `song new` calls `Session.Invoke` directly, not `run`, so that path never sees the New window. Nothing in serve, MCP or the CLI cancels dialogs on its own; serve runs one request at a time, so no other request can cancel it mid-command.
- **Bug found on the way:** `DismissDialog` pressed button 0, "Cancel in every dialog seen so far". In the New window button 0 is OK, so `command run "File > New Session"` would have *started* a song while reporting it cancelled the window. `DismissDialog` now presses the button labeled Cancel (button 0 only when there is none), and `command run "File > New Session"` cancels the window and points at `ucdaw song new`.

## Live test (09:35 NDT, serve restarted with the new build, `--host 192.168.77.78`)

```
$ ucdaw state --json   -> document "2026-10-12 Stephen Clarke", 120 bpm, click on, 1 channel
$ ucdaw song new --tempo 72 --click on                (1.9 s wall)
Started a new song (2026-10-12 Stephen Clarke(2)) at 72 bpm, click on.
$ ucdaw state
72 bpm, 4/4, click on, loop off, stopped; no tracks yet.
$ ucdaw --direct --host 192.168.77.78 state document  (a fresh session, not serve's model)
  document/title   "2026-10-12 Stephen Clarke(2)"
$ ucdaw command run "File > New Session"
File > New Session opened the "New" dialog, which needs choices ucdaw cannot make; canceled it.
  start a new song with: ucdaw song new (MCP new_song)
$ ucdaw state --json   -> still "2026-10-12 Stephen Clarke(2)", 72 bpm (no third song)
```

- A second new song on the same day is named with "(2)" appended, so the title always changes.
- Left as is: the new song "2026-10-12 Stephen Clarke(2)" (unsaved, 72 bpm, click on) is the front tab; "2026-10-12 Stephen Clarke" stays open in its tab. Nothing saved or closed.
- `just test`: 780 tests pass; `golangci-lint`: 0 issues on the ucdaw packages.

# Mixing: analyze, trim, gain staging (2026-10-12, from 10:00 NDT)

Steve's first real trial (remixing "The King Has Come" stems) showed what helps a musician who cannot measure: exact numbers from the stems themselves. His bass export averaged -47 dBFS against drums at -23.7, so no fader move could fix it; a +20 dB input trim did. Guessed fader cuts, peak-normalizing and resetting his balance to 0 dB all failed. This step makes measuring and gain staging part of ucdaw, and writes the engineer's workflow down for Claude.

## Input gain on the wire

- `mixer/channel/chN/inputfx/gain` (dawremote.xml range `InputGain`, -24..24, no curve) travels **normalized like every other ranged value, on a straight line in dB**: read live with a raw session, Bass at +20 dB is wire `0.91667` (44/48) and an untouched channel is `0.5` (0 dB). The trial notes called it "raw dB"; the units are dB, the wire is not. `param set … 20` already did the right conversion, which is why it read back 20.
- `ucdaw trim <track> [dB] [--up|--down dB]` and MCP `trim` set it through the schema and read it back: "Bass trim +20 dB (was 0 dB)." Out of range is refused with the range named.

## Negative levels on the command line

- `ucdaw fader Drums -3` already worked (the fader command reads its own arguments); what failed was `param set <path> -6` ("unknown shorthand flag: '6'"), and `fader … --host X` (the fader parser knew only `--json` and `--direct`).
- Now `fader`, `trim` and `param set` share one parser that takes every global flag (`--json`, `--direct`, `--host`, `--socket`, `--schema`, with or without `=`) anywhere and leaves negative numbers as arguments.

## Measuring (`ucdaw analyze`, MCP `analyze_audio`)

- The header parser now also records where the samples are and how they are stored: WAV format tag (PCM, float, or the SubFormat of `WAVE_FORMAT_EXTENSIBLE`), the `data` offset, AIFC compression (`NONE`/`twos`, `sowt` little-endian, `fl32`/`fl64`), the `SSND` offset. The declared data size is cut to the file's real end. Other AIFC encodings (µ-law, compressed) are refused by name.
- Samples are streamed through a 1 MB buffer; the only thing that grows is one float per 100 ms (capped at ten hours, then "measured the first 10 hours only"). A 302 MB stereo 24-bit stem: 1.28 s, 15 MB resident. Up to four files are measured at once; at most 64 per call.
- Average: plain RMS (a full-scale sine is -3.01 dBFS, not the AES17 +3 dB convention) over 100 ms windows whose RMS is at least -60 dBFS, so the gaps in a sparse part do not drag it down.
- LUFS: ITU-R BS.1770-4. K-weighting biquads are recomputed for the file's rate from libebur128's analog prototypes; 400 ms blocks every 100 ms; -70 LUFS absolute and -10 LU relative gates. Tests: a 997 Hz sine at -20 dBFS reads -20.0 LUFS stereo and -23.0 mono at 44.1, 48 and 96 kHz; 10 kHz reads 3.4 LU above 1 kHz and 30 Hz 9.0 below, as ffmpeg's `ebur128` does. On Steve's eight real stems ucdaw's LUFS equals ffmpeg's to 0.1 LU on every stem (Drums -23.1, Percussion -39.7, Bass -45.5, Acoustic -34.4, Electric -26.5, Keys -34.6, Guides -23.8).
- Silent: a peak below -60 dBFS ("Loops is silent: an empty export (peak -62 dBFS over 4:45). Re-export it before mixing."). Clipping: three or more consecutive full-scale samples in an integer file (a clean full-scale sine touches full scale once per cycle and is not clipping); any sample over full scale in a float file.
- Suggested trim: target minus the average, to 0.1 dB, within -24..+24. When the range is not enough the line says how far short the stem stays (Steve's bass needs +28.5 dB; +24 leaves it 4.5 dB short). Target defaults to -18 dBFS (0 VU on digital consoles and analog-modeled plug-ins); `--target` takes -40 to -6.
- Files and folders are measured on this Mac without Studio Pro (CLI and MCP both skip the engine). A track name measures the stem on that track; no arguments measures every audio track in the open song. Reading only: a test checks nothing is sent to Studio Pro.
- Fuzzed `FuzzAnalyzeReader` (header parse plus full measurement on arbitrary bytes, with random truncation) 20 s, no panic.

## Which file is on which track

- Studio Pro does not tell remotes what a track plays. `ucdaw import` now appends each stem to `imports.json` in the user config folder (song title, track, path, time; 0600, written atomically, at most 2,000 records and 2 MB, control characters stripped, a damaged file ignored). Tests log to a temporary file (TestMain sets `UCDAW_IMPORTS`); an early test run had written test imports into the real log, cleaned out by hand.
- For a song imported before that (Steve's remix), the song's `Cache/Images` waveform files name each audio file and its length in frames (`x<8 hex><file name><frames hex>.peak`). ucdaw names the cached files the way import names tracks (so "The King Has Come 129bpm - Bass.wav" is Bass), then looks for the file in the song's `Media` folder, the folders of logged imports, folders found earlier, and Spotlight (`mdfind -name`, 5 s, 1 MB, 50 results, cloud folders last), and takes the first with the same frame count. The song folder needs no `.song` file (an unsaved song has only `Cache`).
- Spotlight takes about a second per name; folders it finds are remembered for the session, so the whole song (8 stems) measures in 2.9 s through serve the first time and 1.1 s for two stems after.

## Gain staging (`ucdaw gainstage`, MCP `gain_stage`)

- Measures each audio track's stem (or the named tracks) and sets its trim to the suggested value. The trim is computed from the file, so it replaces an existing trim rather than adding to it. Writes only `inputfx/gain` paths (a test fails on any other write); faders are read before and after and the answer says "faders untouched" only if none moved. Silent stems and tracks without a stem are left alone and named. Already at the value: "trim already +5.7 dB", nothing sent.

## Mixing skill and MCP instructions

- `skills/ucdaw/mixing.md` (linked first from SKILL.md's mixing section) is written for Claude as the engineer: measure first; never normalize to peak; never reset the musician's balance without asking; trim fixes the stem, fader sets the balance; ear notes to dB ("a touch" 1-2, "a bit" 3, "a lot" 6); one change at a time; every change with numbers; broken exports flagged; static mix from the foundation; reference at matched loudness; bass judged on headphones, not laptop speakers. Sources cited: Anderton (PC Audio Labs), Nail the Mix, Sound On Sound (Houghton), iZotope, Mastering The Mix, Soundbridge, Owsinski.
- Claude Desktop has no skills, so the MCP server's instructions carry the same workflow in one paragraph, and the `fader`, `trim`, `analyze_audio` and `gain_stage` descriptions say when to use each. 24 tools.

## Live test (10:23–10:30 NDT, Studio Pro 8.0.2, serve restarted with the new build, `--host 192.168.77.78`)

- Read-only against Steve's open remix ("2026-10-12 Stephen Clarke(3)", playing when found; nothing sent to it): `ucdaw analyze` measured all eight tracks from the waveform cache plus Spotlight; numbers as above.
- `ucdaw song new --tempo 129 --click off` → "2026-10-12 Stephen Clarke(4)"; imported the eight base stems (Drums, Percussion, Loops, Bass, Acoustic, Electric, Keys, Guides; not the "(2)"/"(3)" copies or Click) in 10.7 s.
- On the new song: `fader Drums -2`, `fader Keys --down 1.5` (a stand-in for Steve's balance); `trim Bass` 0 → 20 → -6 → `--up 3` → -3; `trim Bass 30` refused; `param set …/ch4/inputfx/gain -12` read back -12.
- `ucdaw gainstage` (2.1 s): Drums +5.7, Percussion +24 (3.5 dB short), Loops left alone (silent), Bass -12 → +24 (4.5 dB short), Acoustic +21.4, Electric +10.8, Keys +19.5, Guides +8.7; "faders untouched". Read back over a fresh `--direct` session: every trim as reported; `state` still shows Drums -2 dB and Keys -1.5 dB. A second `gainstage Drums`: "trim already +5.7 dB".
- MCP over stdio (`ucdaw mcp`): `analyze_audio {tracks: [Bass]}` and `trim {track: Bass}` answered as the CLI does.
- Left as is: the test song "(4)" is the front tab, unsaved; Steve's remix "(3)" is in the previous tab, untouched. Nothing saved or closed; playback not started.
- `just test`: all pass; `golangci-lint`: 0 issues on the ucdaw packages.

# Pre-merge review fixes (2026-10-12, PR #39)

Eight findings from the pre-merge review, each fixed test first. No live run against Studio Pro: every case is reproduced with the in-memory fake Studio Pro.

## 1. MCP tool calls run one at a time

- The go-sdk runs tool calls concurrently. Without `ucdaw serve` nothing serialized them, so selection-based edits (add track then rename the selected one, insert, instrument, audio edits, sections) could interleave and act on the wrong track.
- One process-wide lock in `dawmcp.run` wraps every request. `daw.Request.Exclusive` is the single rule, shared with serve's work lock: everything is exclusive except transport stop, which must never wait behind a timed play.
- Tests: `TestToolCallsReachStudioProOneAtATime`, `TestStopDoesNotWaitBehindAPlay`.

## 2. Dismissing a dialog never chooses for the musician

- `DismissDialog` pressed button 0 when there was no Cancel and reported "canceled". It now presses only a button whose label declines (Cancel, then No, then Don't… / Do Not…, whole words, case-insensitive, typographic apostrophe folded), or the only button of a one-button alert. "Don't Save" is not a decline: it closes without saving and loses work.
- With no such button it presses nothing and returns a Problem naming the dialog and its buttons, hint "answer it in Studio Pro". It returns the label it pressed.
- Every caller (`run`, insert, instrument, audio edits, new song, import) goes through `Studio.dismiss` and says what really happened: `closed it with "Cancel"`, or left open with the buttons named.
- Tests: `TestDismissDialogPressesOnlyADecliningButton`, `TestDialogCallersLeaveAChoiceOpenAndSaySo` (all six callers), `TestDialogCallersNameTheButtonThatClosedIt`.

## 3. Import never answers "Don't Convert"

- The conversion answer took the last label containing a positive word, so "Don't Convert" (or "Don’t Convert") could win. Labels that turn the offer down (cancel, no, don't, do not, without, never, skip, as whole words) are skipped and the first positive one (convert, ok, yes, import, apply, continue) is taken.
- Test: `TestImportAcceptsAConversionAndCancelsAnythingElse` (Don't / Don’t / Do Not / Without / No in either slot).

## 4. A timed play presses Stop only if it is still playing

- Timed play slept and always pressed Stop, stopping a musician who had stopped and restarted playback meanwhile. It now waits on `transport/start` with the deadline and returns as soon as playback stops or restarts elsewhere (any change to `transport/start` after playback began), answering "Playback was stopped elsewhere before the 4 bars were up, so ucdaw did not press Stop." Stop is pressed only if still playing at the deadline. A client hang-up still stops playback (`WithoutCancel`).
- Tests: `TestTimedPlayLeavesAStopAndRestartAlone`, `TestTimedPlayStopsAtTheDeadline`, `TestTimedPlayStopsWhenTheClientHangsUp`.

## 5. Play and import lengths checked as floats

- Seconds and bars were converted to `time.Duration` before the `MaxPlay` check. On amd64 1e300 or +Inf overflows to a negative Duration that passes the check and plays untimed; on arm64 it saturates and the message read "2562047h47m17s is longer…". The length is now compared as a float first (NaN, negatives and -Inf rejected), and a huge length reads "that is longer than the 1m0s limit". The import landing wait (`landingTimeout`, from a header's frame count) is capped the same way.
- Tests: `TestPlayRejectsLengthsThatOverflow` (+Inf, 1e300, 1e19, 60.5 s, -Inf, NaN, -1; seconds, bars and section play), `TestLandingTimeoutCapsHugeHeaders`.

## 6. Arranger edits confirmed by the whole list

- Delete waited for the count only, so a wrong section removed read as success; move deleted the original after checking only the count of the paste. Every arranger edit (duplicate, repeat/copy, move, delete, rename, add) now waits for the exact list it expects, names and starts, and otherwise reports what Studio Pro shows against what was expected. Move checks the pasted copy (source name at the destination, later sections shifted) before deleting the original, so a bad paste never costs the original.
- Tests: `TestSectionDeleteChecksTheWholeList`, `TestSectionMoveChecksTheWholeList`, `TestSectionMoveKeepsTheOriginalWhenTheCopyIsWrong`, `TestSectionCopyAndDuplicateCheckTheWholeList`, `TestSectionRenameChecksTheWholeList`.

## 7. Dialog button slots stay positional

- An unlabeled first button was dropped, moving the second into slot 0, so the `dialogResult` sent pressed the wrong button. Slots now stay in place (an empty label kept as ""); only trailing empty slots go. Matching skips empty labels; "one-button alert" counts labeled buttons.
- Test: `TestDialogButtonsKeepTheirSlots`, plus the `["", "Cancel"]` case in finding 2's test.

## 8. Spike scaffolding removed

- `docs/superpowers/plans/2026-07-27-studio-pro-ucnet-poc.md` and `docs/superpowers/specs/2026-07-27-studio-pro-ucnet-poc-design.md` described the `ucdawpoc` proof that `ucdaw` superseded. Their protocol findings were already in Hugo `knowledge/ucnet-studio-pro/protocol.md` and this log; the one thing missing there, how the traffic was captured (`rvictl` remote virtual interface on the iPad), was added to Hugo first. Both files removed.

## 9. Dialogs that could lose work are never answered

- On "Save changes?" a No button discards the song, and labels alone cannot tell which button keeps the work. A dialog whose title or text mentions saving, discarding, deleting, removing, overwriting or closing (any form: "saving", "deleted", "overwrite", "closing"; case-insensitive) is left open whatever its buttons, even Cancel or a single OK, and the Problem names the dialog and its buttons with the hint "answer it in Studio Pro". Import does not answer a conversion question that mentions overwriting.
- Tests: `TestDismissNeverAnswersADialogThatCouldLoseWork`, `TestImportNeverAnswersAConversionThatOverwrites`; `TestNewSongCancelsAnotherDialog` now expects its "Save Changes" dialog left open.

## 10. Finding 6 checked live (11:52–11:58 NDT, Studio Pro 8.0.2, serve restarted with the new build, `--host 192.168.77.78`)

- Only on a new song made for the test: `ucdaw song new --tempo 120` → "Started a new song (2026-10-12 Stephen Clarke(5)) at 120 bpm, click off." Before every step the script read `document/title` and would have stopped had the front song been anything else. Steve's remix and the other test songs were not touched; nothing saved or closed.
- **Found: Studio Pro makes no section in a song with no tracks.** The first `section add Verse Chorus Bridge --bars 4` on the empty song answered "Adding section Verse was sent, but Studio Pro's arranger did not show it; sections now: none." (the count check, not the new list check: nothing arrived at all). After `ucdaw track add --name Guide` the same add worked. `section add` now refuses in a song with no tracks and says to add one (`TestSectionAddNeedsATrack`).
- Every edit after that passed the whole-list check against the real arranger; no widening was needed (Studio Pro shifts later sections by exactly the section length). Output:

```
$ ucdaw track add --name Guide
Added mono audio track Guide.
$ ucdaw section add Verse --bars 4
Added section Verse, 4 bars each; sections now: Verse bar 1.
$ ucdaw section add Chorus Bridge --bars 4
Added sections Chorus and Bridge, 4 bars each; sections now: Verse bar 1, Chorus bar 5, Bridge bar 9.
$ ucdaw section list
3 sections: Verse bar 1 (4 bars), Chorus bar 5 (4 bars), Bridge bar 9 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Chorus bar 5, Bridge bar 9.

$ ucdaw section rename Bridge Outro
Renamed Bridge to Outro.
$ ucdaw section list
3 sections: Verse bar 1 (4 bars), Chorus bar 5 (4 bars), Outro bar 9 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Chorus bar 5, Outro bar 9.

$ ucdaw section duplicate Verse
Duplicated Verse; the copy starts at bar 5 and the rest moved 4 bars later.
$ ucdaw section list
4 sections: Verse bar 1 (4 bars), Verse bar 5 (4 bars), Chorus bar 9 (4 bars), Outro bar 13 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Verse bar 5, Chorus bar 9, Outro bar 13.

$ ucdaw section move Chorus after Outro
Moved Chorus after Outro; it now starts at bar 13 (undo takes two steps).
$ ucdaw section list
4 sections: Verse bar 1 (4 bars), Verse bar 5 (4 bars), Outro bar 9 (4 bars), Chorus bar 13 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Verse bar 5, Outro bar 9, Chorus bar 13.

$ ucdaw section delete Verse 2
Deleted Verse (bar 5, 4 bars); the rest moved up to close the gap.
$ ucdaw section list
3 sections: Verse bar 1 (4 bars), Outro bar 5 (4 bars), Chorus bar 9 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Outro bar 5, Chorus bar 9.

$ ucdaw section repeat Chorus after Verse
Repeated Chorus after Verse, at bar 5; the rest moved 4 bars later.
$ ucdaw section list
4 sections: Verse bar 1 (4 bars), Chorus bar 5 (4 bars), Outro bar 9 (4 bars), Chorus bar 13 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Verse bar 1, Chorus bar 5, Outro bar 9, Chorus bar 13.

$ ucdaw section move Outro before Verse
Moved Outro before Verse; it now starts at bar 1 (undo takes two steps).
$ ucdaw section list
4 sections: Outro bar 1 (4 bars), Verse bar 5 (4 bars), Chorus bar 9 (4 bars), Chorus bar 13 (4 bars).
$ ucdaw state
120 bpm, 4/4, click off, loop off, stopped; 1 channel: Guide (audio, mono); sections: Outro bar 1, Verse bar 5, Chorus bar 9, Chorus bar 13.

```

- Wrong edits still fail: Studio Pro cannot be made to misbehave on purpose, so the failing cases stay covered by the fake arranger tests (wrong section deleted, wrong paste, no shift, extra rename).
- Left as is: the test song "(5)" is the front tab, unsaved, with its Guide track and four sections. Nothing saved or closed; playback not started.
