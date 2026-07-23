#!/usr/bin/env bats
# End-to-end coverage for the read commands: dump and get.

load test_helper

@test "dump prints seeded paths and values" {
  run "${UCMIX_BIN}" dump
  [ "$status" -eq 0 ]
  [[ "$output" == *"global/mixer_name"* ]]
  [[ "$output" == *"TestBoard"* ]]
  [[ "$output" == *"line/ch1/username"* ]]
  # mute humanizes to a bool
  [[ "$output" == *"line/ch2/mute"* ]]
  [[ "$output" == *"true"* ]]
}

@test "dump [prefix] filters to matching paths" {
  run "${UCMIX_BIN}" dump line
  [ "$status" -eq 0 ]
  [[ "$output" == *"line/ch1/username"* ]]
  [[ "$output" != *"global/mixer_name"* ]]
  [[ "$output" != *"main/ch1/volume"* ]]
}

@test "dump --raw shows wire values" {
  run "${UCMIX_BIN}" dump line --raw
  [ "$status" -eq 0 ]
  # raw mute is a numeric 0/1, not a bool word
  [[ "$output" == *"line/ch1/mute"* ]]
  [[ "$output" == *"line/ch1/volume"* ]]
  [[ "$output" == *"0.74"* ]]
}

@test "dump --json is a valid JSON object" {
  run "${UCMIX_BIN}" dump --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  # object keyed by path
  [[ "$output" == *"\"line/ch1/username\""* ]]
}

@test "get returns one humanized value" {
  run "${UCMIX_BIN}" get line.ch2.mute
  [ "$status" -eq 0 ]
  [[ "$output" == *"line/ch2/mute"* ]]
  [[ "$output" == *"true"* ]]
}

@test "get --raw returns the wire value" {
  run "${UCMIX_BIN}" get line.ch1.volume --raw
  [ "$status" -eq 0 ]
  [[ "$output" == *"0.74"* ]]
}

@test "get --json emits an envelope" {
  run "${UCMIX_BIN}" get line.ch1.username --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"path\""* ]]
  [[ "$output" == *"\"value\""* ]]
  [[ "$output" == *"Kick"* ]]
}

@test "get on a missing path fails with a hint" {
  run "${UCMIX_BIN}" get line.ch9.mute
  [ "$status" -ne 0 ]
  [[ "$output" == *"path not found"* ]]
  [[ "$output" == *"ucmix dump"* ]]
}
