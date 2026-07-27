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

The capture established that bytes 4–5 of the `DA` announcement are the
little-endian TCP control port. For the current session they decode to `49601`.
Byte 8 supplies the server side of the UCNET connection identity. Both values
must be read from each live announcement.

These values are evidence from the current session, not permanent
configuration. The PoC must identify the endpoint from its announcement rather
than hard-code its IP address or source port.

## Approach

Use the working Studio Pro Remote app as the protocol reference:

1. Capture Studio Pro Remote traffic from the iPad's remote virtual interface.
2. Connect Studio Pro Remote from the iPad.
3. In the app, press Play, wait about one second, and press Stop.
4. Separate discovery, connection setup, state updates, Play, and Stop by packet
   order and payload differences.
5. Save only the smallest protocol fixtures needed for automated tests. Remove
   volatile addresses, device identifiers, session names, and unrelated state.
6. Implement the minimum client that can reproduce the observed Play and Stop
   exchange.

The captured control session is plain UCNET over TCP. It queries the user
license, advertises a local UDP metering port, subscribes as `dawremote`, and
uses a three-frame `PE`, `PV`, `PE` gesture for each transport action. Studio
Pro confirms each action with matching `PV` transport state.

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
- Send `PV` only for the captured `transport/start` and `transport/stop`
  gestures. Do not send mixer parameter paths, mixer subscription fields,
  `PS`, or `PC` writes to the DAW.
- Do not store a general network capture in the repository.

## Tests

Implementation follows test-first development:

1. A captured DAW announcement decodes to the observed device class, identity,
   version label, display name, host, TCP control port, and connection identity.
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
