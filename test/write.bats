#!/usr/bin/env bats
# End-to-end coverage for the set command and its value parsing.

load test_helper

@test "set a boolean and read it back" {
  run "${UCMIX_BIN}" set line.ch1.mute on
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch1/mute = true"* ]]

  run "${UCMIX_BIN}" get line.ch1.mute
  [ "$status" -eq 0 ]
  [[ "$output" == *"true"* ]]
}

@test "set a fader level in dB round-trips on the wire" {
  run "${UCMIX_BIN}" set line.ch1.volume -6dB
  [ "$status" -eq 0 ]
  [[ "$output" == *"set line/ch1/volume"* ]]

  # -6 dB is wire position 0.746 (the Fader taper anchor).
  run "${UCMIX_BIN}" get line.ch1.volume --raw
  [ "$status" -eq 0 ]
  [[ "$output" == *"0.74"* ]]
}

@test "set a string name and read it back" {
  run "${UCMIX_BIN}" set line.ch1.username "Kick Drum"
  [ "$status" -eq 0 ]

  run "${UCMIX_BIN}" get line.ch1.username
  [ "$status" -eq 0 ]
  [[ "$output" == *"Kick Drum"* ]]
}

@test "set a hex color appends the alpha byte" {
  run "${UCMIX_BIN}" set line.ch1.color 4ed2ff
  [ "$status" -eq 0 ]
  # RGB gains a 0xff alpha -> 4ed2ffff.
  [[ "$output" == *"4ed2ffff"* ]]
}

@test "set then get a color reads back as the same hex" {
  run "${UCMIX_BIN}" set line.ch1.color 9478ce
  [ "$status" -eq 0 ]
  # Read it back on a fresh snapshot: the packed integer must humanize to hex,
  # symmetric with the write echo (not a raw ABGR-packed int).
  run "${UCMIX_BIN}" get line.ch1.color
  [ "$status" -eq 0 ]
  [[ "$output" == *"9478ceff"* ]]
}

@test "set --json emits color as hex, not base64" {
  run "${UCMIX_BIN}" set --json line.ch1.color 4ed2ff
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"4ed2ffff"* ]]
  [[ "$output" != *"=="* ]]
}

@test "set --json reports the write" {
  run "${UCMIX_BIN}" set --json line.ch1.mute off
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"ok\""* ]]
  [[ "$output" == *"line/ch1/mute"* ]]
}

@test "set with an invalid value fails with a hint" {
  run "${UCMIX_BIN}" set line.ch1.mute maybe
  [ "$status" -ne 0 ]
  [[ "$output" == *"invalid value"* ]]
  [[ "$output" == *"set --help"* ]]
}

@test "set several key=value pairs writes them all over one connection" {
  run "${UCMIX_BIN}" set line.ch1.mute=on line.ch2.mute=on line.ch3.username=Vox
  [ "$status" -eq 0 ]
  [[ "$output" == *"set 3 values"* ]]

  run "${UCMIX_BIN}" get line.ch1.mute
  [[ "$output" == *"true"* ]]
  run "${UCMIX_BIN}" get line.ch2.mute
  [[ "$output" == *"true"* ]]
  run "${UCMIX_BIN}" get line.ch3.username
  [[ "$output" == *"Vox"* ]]
}

@test "set -f writes every path value line in a file" {
  cat >writes.txt <<'EOF'
# a channel strip
line.ch1.mute on
line.ch1.volume -6dB

line.ch1.username Kick Drum
EOF
  run "${UCMIX_BIN}" set -f writes.txt
  [ "$status" -eq 0 ]
  [[ "$output" == *"set 3 values"* ]]

  run "${UCMIX_BIN}" get line.ch1.username
  [[ "$output" == *"Kick Drum"* ]]
  run "${UCMIX_BIN}" get line.ch1.volume --raw
  [[ "$output" == *"0.74"* ]]
}

@test "set --json for a batch emits a settings array" {
  run "${UCMIX_BIN}" set --json line.ch1.mute=on line.ch2.mute=off
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"written\""* ]]
  [[ "$output" == *"line/ch1/mute"* ]]
  [[ "$output" == *"line/ch2/mute"* ]]
}

@test "set -f with an unparseable line fails with a hint" {
  printf 'line.ch1.mute\n' >bad.txt
  run "${UCMIX_BIN}" set -f bad.txt
  [ "$status" -ne 0 ]
  [[ "$output" == *"path value"* ]]
}
