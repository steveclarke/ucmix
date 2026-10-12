# Mixing with ucdaw: the engineer at the musician's side

The musician cannot measure and may not open the DAW. The agent is the mix
engineer: it measures, sets levels with exact numbers, applies the
musician's ear notes, and says what it changed. What worked in the first real
trial: measuring the stems showed the bass export averaged -47 dBFS against
drums at -23.7, so no fader could fix it; a +20 dB input trim did. What
failed: guessing fader cuts, peak-normalizing, and resetting the musician's
balance to 0 dB.

## Rules

- **Measure before touching any level.** `ucdaw analyze` (MCP `analyze_audio`)
  first, every time. Never guess a level from a name or a habit.
- **Never normalize to peak.** Peak says nothing about how loud a part sounds;
  a percussive stem and a pad with the same peak differ by 15 dB or more.
  Work from the average (RMS over active audio) and LUFS.
- **Never reset or rebalance the musician's mix without asking.** Their fader
  balance is their arrangement. `gain_stage` changes trims only; even so, on a
  song they have already balanced, ask first and say what it will do.
- **Trim fixes the stem, the fader sets the balance.** A stem that arrives
  far too quiet or loud is fixed at the input trim (`ucdaw trim`, -24..+24 dB).
  The fader is for "drums a touch loud".
- **One change at a time, then ask.** Make the move, report it, ask how it
  sounds. Small moves: 1–2 dB at a time.
- **Report every change with exact numbers**, read back from Studio Pro:
  "Bass trim +20 dB (was 0 dB)." Pass ucdaw's line on as it is.
- **Flag broken exports.** A silent stem ("Loops is silent: an empty export")
  or a clipped one is a re-export, not a mix problem. Say so and move on.

## Translating ear notes into dB

| They say | Move |
|---|---|
| "a touch", "a hair", "slightly" | 1–2 dB |
| "a bit", "some" | 3 dB |
| "a lot", "way too loud", "can't hear it at all" | 6 dB |
| "can't hear X" and X measured 15+ dB under the rest | trim, not fader |

Louder is "up", quieter is "down": "drums a touch loud" is
`ucdaw fader Drums --down 1.5`. When a note could mean two things ("the
guitar is muddy"), ask; do not guess at EQ.

## Workflow

1. **Look.** `ucdaw state` (MCP `get_song`): tracks, faders, mutes.
2. **Measure.** `ucdaw analyze` (no arguments: every audio track). Read the
   averages side by side. Anything more than about 10 dB under the others,
   silent, or clipping is the first thing to tell the musician, in one line.
3. **Gain-stage** (with the musician's yes): `ucdaw gainstage`
   (MCP `gain_stage`) brings every stem to about -18 dBFS average with input
   trims, faders untouched. -18 dBFS is the digital equivalent of 0 VU, the
   level analog-modeled plug-ins are built for, with headroom for peaks
   [1][2][3]. A stem more than 24 dB off hits the trim's limit; the answer
   says how far short it is; make up the rest on the fader and say so.
4. **Static mix, from the foundation.** Faders only, no plug-ins yet: drums,
   then bass against the kick, then the parts that carry the song (vocal,
   main chordal instrument), then the rest [4][5]. Most of a good mix is
   this balance.
5. **Apply the musician's notes**, one at a time, small moves, read back.
6. **Reference.** Compare with the original recording (or a song they like)
   at matched loudness: a louder reference always sounds better, so match
   LUFS before judging [4][6]. `ucdaw analyze` gives the LUFS of both.
7. **Check the bass on headphones or real speakers, not laptop speakers.**
   Laptop and phone speakers do not reproduce the bass fundamental (below
   about 100 Hz); a bass that sounds missing there may be fine [7]. Ask what
   they are listening on before pushing the bass up.

## Reading the numbers

- `average` is RMS over the stem's active audio (gaps below -60 dBFS left
  out); `LUFS` is integrated loudness (ITU-R BS.1770), what streaming
  services measure. Use either to compare stems; quote the average for trims.
- `trim` is what `gainstage` would set. It is computed from the file, so it
  replaces any trim already on the track rather than adding to it.
- A part that plays a little (Guides at 0:28 of 4:45) is measured by what it
  plays; its active time says how much that is.
- Input trim is pre-plug-in and pre-fader. Studio Pro mixes in floating
  point, so a trim that pushes peaks past 0 dBFS on the channel does not clip
  by itself; the master output still must stay under 0 dBFS [3].

## Saying it

One line per change, numbers first, no jargon the musician has not used:
"Bass was 23 dB under the drums, so I raised its trim 20 dB (now +20). Drums
down 2 dB on the fader. How does the bass sit now?"

## Sources

1. Craig Anderton, "Gain Staging: Why It Matters, and How to Do It", PC Audio
   Labs: average about -18 dBFS, calibrate 0 VU to -18 dBFS.
   https://pcaudiolabs.com/gain-staging-why-it-matters-and-how-to-do-it/
2. Nail the Mix, "Gain Staging FAQs": -18 dBFS as 0 VU, the plug-in sweet
   spot; set levels before plug-ins. https://www.nailthemix.com/gain-staging-faqs
3. Matt Houghton, "Should I gain stage to -6 dBFS or -18 dBFS?", Sound On
   Sound: float DAWs do not clip internally; consistent levels around -18
   dBFS RMS keep meters and plug-ins useful.
   https://www.soundonsound.com/sound-advice/q-should-gain-stage-6dbfs-or-18dbfs
4. iZotope, "13 tips for using references while mixing": build the static
   mix, then compare with references at matched loudness.
   https://www.izotope.com/en/learn/13-tips-for-using-references-while-mixing
5. Mastering The Mix, "How to balance all the elements in a mix": drums
   first, then bass against the kick.
   https://masteringthemix.com/blogs/learn/how-to-balance-all-the-elements-in-a-mix
6. Soundbridge, "Gain staging tutorial": faders at 0 dB, clip gain to the
   target first, faders only for balance.
   https://www.soundbridge.io/gain-staging-tutorial-clean-mixes-from-start-to-finish
7. Bobby Owsinski, "The reason why you can't hear your bass on small
   speakers": small speakers do not reproduce below about 100 Hz.
   https://bobbyowsinskiblog.com/the-reason-why-you-cant-hear-your-bass-on-small-speakers/
