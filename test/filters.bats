#!/usr/bin/env bats
# End-to-end coverage for the scope-filter tiles: what a store, recall or reset
# touches. Read and write both go through ordinary parameter keys.

load test_helper

@test "filters ls shows every group" {
  run "${UCMIX_BIN}" filters ls
  [ "$status" -eq 0 ]
  [[ "$output" == *"scene"* ]]
  [[ "$output" == *"advanced"* ]]
  [[ "$output" == *"project"* ]]
}

# The seeded board has 48v excluded (the board's own default) and mute included.
@test "filters ls reports a tile's state" {
  run "${UCMIX_BIN}" filters ls scene
  [ "$status" -eq 0 ]
  [[ "$output" == *"48v"* ]]
  [[ "$output" == *"excluded"* ]]
  [[ "$output" == *"included"* ]]
}

@test "filters ls one group leaves the others out" {
  run "${UCMIX_BIN}" filters ls project
  [ "$status" -eq 0 ]
  [[ "$output" == *"inputpatching"* ]]
  [[ "$output" != *"eqdynins"* ]]
}

@test "filters ls rejects an unknown group" {
  run "${UCMIX_BIN}" filters ls nosuchgroup
  [ "$status" -ne 0 ]
  [[ "$output" == *"filter group"* ]]
}

@test "filters ls --json emits an envelope" {
  run "${UCMIX_BIN}" filters ls scene --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *'"tile": "48v"'* ]]
  [[ "$output" == *'"path": "global/fltr48v"'* ]]
}

@test "filters set includes a tile and it reads back" {
  run "${UCMIX_BIN}" filters set scene 48v on
  [ "$status" -eq 0 ]
  [[ "$output" == *"48v"* ]]
  [[ "$output" == *"included"* ]]

  run "${UCMIX_BIN}" get global/fltr48v
  [ "$status" -eq 0 ]
  [[ "$output" == *"true"* ]]
}

@test "filters set excludes a tile" {
  run "${UCMIX_BIN}" filters set scene mute off
  [ "$status" -eq 0 ]
  [[ "$output" == *"excluded"* ]]

  run "${UCMIX_BIN}" get global/fltrmute
  [ "$status" -eq 0 ]
  [[ "$output" == *"false"* ]]
}

@test "filters set accepts a dashed tile name" {
  run "${UCMIX_BIN}" filters set advanced dca-groups off
  [ "$status" -eq 0 ]
  [[ "$output" == *"excluded"* ]]
}

@test "filters set rejects an unknown tile" {
  run "${UCMIX_BIN}" filters set scene phantom on
  [ "$status" -ne 0 ]
  [[ "$output" == *"tile"* ]]
}

@test "filters set rejects a value that is not on or off" {
  run "${UCMIX_BIN}" filters set scene 48v maybe
  [ "$status" -ne 0 ]
  [[ "$output" == *"on"* ]]
}

@test "filters set --json emits an envelope" {
  run "${UCMIX_BIN}" filters set scene 48v off --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *'"included": false'* ]]
}
