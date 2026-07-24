#!/usr/bin/env bats
# End-to-end coverage for scene commands: store, recall, reset, ls.

load test_helper

@test "store then recall a scene" {
  run "${UCMIX_BIN}" store proj1 sceneA
  [ "$status" -eq 0 ]
  [[ "$output" == *"stored proj1 / sceneA"* ]]

  run "${UCMIX_BIN}" recall proj1 sceneA
  [ "$status" -eq 0 ]
  [[ "$output" == *"recalled proj1 / sceneA"* ]]
}

@test "recall --json emits an envelope" {
  run "${UCMIX_BIN}" recall proj1 sceneA --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"action\""* ]]
  [[ "$output" == *"recall"* ]]
}

@test "ls projects lists the board's projects" {
  run "${UCMIX_BIN}" ls projects
  [ "$status" -eq 0 ]
  [[ "$output" == *"Main Live"* ]]
  [[ "$output" == *"Rehearsal"* ]]
  # Empty slots are dropped, not listed.
  [[ "$output" != *"Empty Location"* ]]
}

@test "ls projects --json is valid JSON" {
  run "${UCMIX_BIN}" ls projects --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"projects\""* ]]
  [[ "$output" == *"Main Live"* ]]
}

@test "ls scenes lists a project's scenes" {
  run "${UCMIX_BIN}" ls scenes "01.Main Live.proj"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Opening Set"* ]]
  [[ "$output" == *"Encore"* ]]
  # The .cnfg entry and empty slots are dropped.
  [[ "$output" != *".cnfg"* ]]
  [[ "$output" != *"Empty Location"* ]]
}

@test "ls scenes --json is valid JSON" {
  run "${UCMIX_BIN}" ls scenes "01.Main Live.proj" --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"scenes\""* ]]
}

@test "reset without --yes refuses in a non-tty" {
  run "${UCMIX_BIN}" reset </dev/null
  [ "$status" -ne 0 ]
  [[ "$output" == *"destructive"* ]]
  [[ "$output" == *"--yes"* ]]
}

@test "reset --yes proceeds" {
  run "${UCMIX_BIN}" reset --yes
  [ "$status" -eq 0 ]
  [[ "$output" == *"reset mixer"* ]]
}

@test "reset --yes --json emits an envelope" {
  run "${UCMIX_BIN}" reset --yes --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"action\""* ]]
  [[ "$output" == *"reset"* ]]
}

@test "reset --scene --yes resets only the scene scope" {
  run "${UCMIX_BIN}" reset --scene --yes
  [ "$status" -eq 0 ]
  [[ "$output" == *"scene"* ]]
}
