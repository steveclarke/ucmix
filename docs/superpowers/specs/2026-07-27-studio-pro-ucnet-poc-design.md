# Studio Pro UCNET Transport PoC

## Goal

Prove whether this repository can use UCNET to control the transport in a
running Fender Studio Pro session. The proof is complete when a local command
finds the current Studio Pro endpoint, starts playback briefly, and stops it
again.

This is a protocol spike, not a supported `ucmix` feature. It must not change
mixer state, arm recording, or expose unverified DAW controls.

## Observed Endpoint

Studio Pro 8.1.1 is currently announcing itself over UCNET:

- Host: `192.168.77.129`
- Device class: `DAW`
- Identity: `ni5f6funpcuob3by.studioapp8`
- Display name: `Steve's Mac mini`
- Discovery message: `DA`
- Discovery transport: UDP

The UDP source port in the announcement is dynamic and is not a TCP listener.
The existing discovery parser accepts the announcement because it shares the
UCNET header, but incorrectly presents every accepted device as a mixer.

These values are evidence from the current session, not permanent
configuration. The PoC must identify the endpoint from its announcement rather
than hard-code its IP address or source port.

## Approach

Use the working Studio Pro Remote app as the protocol reference:

1. On the local gateway, capture only UDP packets involving the DAW host.
2. Connect Studio Pro Remote from the iPad.
3. In the app, press Play, wait about one second, and press Stop.
4. Separate discovery, connection setup, state updates, Play, and Stop by packet
   order and payload differences.
5. Save only the smallest protocol fixtures needed for automated tests. Remove
   volatile addresses, device identifiers, session names, and unrelated state.
6. Implement the minimum client that can reproduce the observed Play and Stop
   exchange.

If the capture is encrypted or does not distinguish the commands, make one
small active probe at a time against the advertised UDP endpoint. MIDI/MMC is a
fallback for proving general remote control, but it does not satisfy this
UCNET-specific proof.

## Code Boundary

Add an unpublished `ucdawpoc` command under `cmd/`. It may reuse generic UCNET
packet framing from `internal/proto`, but DAW-specific discovery, messages, and
state must remain separate from the StudioLive mixer client.

The command surface is intentionally small:

```text
ucdawpoc discover
ucdawpoc transport play
ucdawpoc transport stop
```

`discover` reports only DAW announcements. A transport command selects a single
live endpoint whose device class is `DAW` and whose identity ends in
`.studioapp8`. Multiple matches or no match are errors.

If the captured protocol requires a held session, one command invocation may
open the session, send its transport action, wait for the observed
acknowledgment or state update, and close it. The PoC must not invent an
acknowledgment when none exists.

## Safety

- No Record, arm, save, quit, locate, or mixer parameter commands.
- Send Stop before the live Play test.
- After Play, send Stop within one second even if normal verification fails.
- Handle interruption by making a best-effort Stop before exit.
- Reject non-DAW announcements and unexpected Studio Pro identities.
- Do not send mixer `Subscribe`, `PV`, `PS`, or `PC` writes to the DAW.
- Do not store a general network capture in the repository.

## Tests

Implementation follows test-first development:

1. A captured DAW announcement decodes to the observed device class, identity,
   version label, display name, host, and dynamic UDP port.
2. Mixer and malformed announcements are rejected by DAW discovery.
3. Captured Play and Stop messages decode to distinct transport actions.
4. Encoding each supported action reproduces its sanitized captured fixture.
5. Endpoint selection rejects zero, multiple, and non-`studioapp8` devices.
6. The live-test wrapper always schedules Stop after Play.

Tests use sanitized packet fixtures and local UDP sockets. Routine tests never
contact the Mac Mini, iPad, gateway, or mixer.

## Live Verification

With a safe Studio Pro session open:

1. Run DAW discovery and confirm it reports `Steve's Mac mini`.
2. Send Stop and confirm the session is stopped.
3. Send Play and observe playback begin.
4. Within one second, send Stop and observe playback end.
5. Confirm the StudioLive mixer state was not changed.

Report separately whether command receipt is confirmed by a UCNET response,
transport state update, or only visible behavior in Studio Pro.

## Deferred

The PoC does not add DAW commands to the published `ucmix` CLI. It also defers
track enumeration, faders, plug-ins, macros, markers, sessions, authentication,
and long-lived subscriptions. Those need a separate design after the transport
protocol is proven.
