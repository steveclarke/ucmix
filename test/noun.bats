#!/usr/bin/env bats
# End-to-end coverage for the noun-grouped convenience commands: channel, mix,
# and send. Each is a thin veneer over set — these confirm the verb→path mapping
# and value handling against the fakeboard.

load test_helper

# --- channel ---

@test "channel name sets line/ch{n}/username" {
  run "${UCMIX_BIN}" channel 3 name "Lead Vox"
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch3/username = Lead Vox"* ]]

  run "${UCMIX_BIN}" get line/ch3/username
  [ "$status" -eq 0 ]
  [[ "$output" == *"Lead Vox"* ]]
}

@test "channel fader takes a dB value" {
  run "${UCMIX_BIN}" channel 1 fader -6dB
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch1/volume = -6"* ]]

  run "${UCMIX_BIN}" get line/ch1/volume --raw
  [ "$status" -eq 0 ]
  [[ "$output" == *"0.74"* ]]
}

@test "channel phantom maps to 48v" {
  run "${UCMIX_BIN}" channel 1 phantom on
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch1/48v = true"* ]]
}

@test "channel color resolves a name to hex" {
  run "${UCMIX_BIN}" channel 3 color blue
  [ "$status" -eq 0 ]
  [[ "$output" == *"line/ch3/color = 4ed2ffff"* ]]
}

@test "channel color still accepts hex" {
  run "${UCMIX_BIN}" channel 3 color 9478ce
  [ "$status" -eq 0 ]
  [[ "$output" == *"9478ceff"* ]]
}

@test "channel icon resolves a name to an id" {
  run "${UCMIX_BIN}" channel 1 icon drums
  [ "$status" -eq 0 ]
  [[ "$output" == *"line/ch1/iconid = drums/drumset"* ]]
}

@test "channel with an unknown verb fails with a hint" {
  run "${UCMIX_BIN}" channel 1 frobnicate x
  [ "$status" -ne 0 ]
  [[ "$output" == *"unknown channel verb"* ]]
  [[ "$output" == *"channel --help"* ]]
}

@test "channel --json reports the write" {
  run "${UCMIX_BIN}" --json channel 1 mute on
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"line/ch1/mute"* ]]
  [[ "$output" == *"\"ok\""* ]]
}

# --- mix ---

@test "mix by number maps to aux/ch{n}" {
  run "${UCMIX_BIN}" mix 1 fader -6dB
  [ "$status" -eq 0 ]
  [[ "$output" == *"set aux/ch1/volume = -6"* ]]
}

@test "mix by name resolves against aux usernames" {
  # seed names aux 1 "Steve".
  run "${UCMIX_BIN}" mix Steve fader -6dB
  [ "$status" -eq 0 ]
  [[ "$output" == *"set aux/ch1/volume = -6"* ]]
}

@test "mix by an unknown name fails with a hint" {
  run "${UCMIX_BIN}" mix Ghost fader -6dB
  [ "$status" -ne 0 ]
  [[ "$output" == *"no mix named"* ]]
}

@test "mix limiter writes limiteron plus threshold and release" {
  run "${UCMIX_BIN}" mix 1 limiter on --threshold -6 --release 400
  [ "$status" -eq 0 ]
  # A multi-write reports a summary line and a path/value table.
  [[ "$output" == *"set 3 values"* ]]
  [[ "$output" == *"aux/ch1/limit/limiteron"* ]]
  [[ "$output" == *"true"* ]]
  [[ "$output" == *"aux/ch1/limit/threshold"* ]]
  [[ "$output" == *"aux/ch1/limit/release"* ]]
}

@test "mix limiter --json emits the batch write envelope" {
  run "${UCMIX_BIN}" --json mix 1 limiter on --threshold -6
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"written\""* ]]
  [[ "$output" == *"aux/ch1/limit/limiteron"* ]]
  [[ "$output" == *"aux/ch1/limit/threshold"* ]]
}

# --- send ---

@test "send maps to line/ch{ch}/aux{mix}" {
  run "${UCMIX_BIN}" send 3 1 -6dB
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch3/aux1 = -6"* ]]

  run "${UCMIX_BIN}" get line/ch3/aux1 --raw
  [ "$status" -eq 0 ]
  [[ "$output" == *"0.74"* ]]
}

@test "send with a bad channel number fails" {
  run "${UCMIX_BIN}" send 0 1 -6dB
  [ "$status" -ne 0 ]
  [[ "$output" == *"invalid channel number"* ]]
}
