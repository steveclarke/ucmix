#!/usr/bin/env bats
# End-to-end coverage for the project layer: listing projects and reporting
# which one the board has loaded.

load test_helper

@test "project ls lists projects with their titles" {
  run "${UCMIX_BIN}" project ls
  [ "$status" -eq 0 ]
  [[ "$output" == *"01.Main Live.proj"* ]]
  [[ "$output" == *"Main Live"* ]]
  [[ "$output" == *"Rehearsal"* ]]
  # Empty slots are not projects.
  [[ "$output" != *"Empty Location"* ]]
}

# The seeded board has 01.Main Live.proj loaded.
@test "project ls marks the loaded project" {
  run "${UCMIX_BIN}" project ls
  [ "$status" -eq 0 ]
  [[ "$output" == *"loaded"* ]]
}

@test "project ls --json emits an envelope" {
  run "${UCMIX_BIN}" project ls --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *'"name": "01.Main Live.proj"'* ]]
  [[ "$output" == *'"loaded": true'* ]]
}

@test "project store and recall are not implemented" {
  run "${UCMIX_BIN}" project store "Main Live"
  [ "$status" -ne 0 ]
  run "${UCMIX_BIN}" project recall "Main Live"
  [ "$status" -ne 0 ]
}
