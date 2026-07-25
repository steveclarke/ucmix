#!/usr/bin/env bats
# End-to-end coverage for scene commands: store, recall, reset, ls.

load test_helper

# Project and scene arguments are display titles or the board's slot names. A
# bare made-up name is not a path the board honors, so it must fail loudly.
@test "store a new scene into the next free slot" {
  run "${UCMIX_BIN}" store "Main Live" "Soundcheck"
  [ "$status" -eq 0 ]
  [[ "$output" == *"stored Main Live / Soundcheck"* ]]
  # The slot the write landed on is named, since a store is destructive.
  [[ "$output" == *"slot 03.Soundcheck.scn"* ]]

  # It is listable afterwards, i.e. it really landed.
  run "${UCMIX_BIN}" ls scenes "01.Main Live.proj"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Soundcheck"* ]]
}

@test "store refuses to overwrite an existing scene without --replace" {
  run "${UCMIX_BIN}" store "Main Live" "Opening Set"
  [ "$status" -ne 0 ]
  [[ "$output" == *"already"* ]]
  [[ "$output" == *"--replace"* ]]
}

@test "store --replace names the scene it displaced" {
  run "${UCMIX_BIN}" store "Main Live" "Opening Set" --replace
  [ "$status" -eq 0 ]
  [[ "$output" == *"replacing"* ]]
  [[ "$output" == *"Opening Set"* ]]
  [[ "$output" == *"01.Opening Set.scn"* ]]
}

@test "store rejects an unknown project" {
  run "${UCMIX_BIN}" store nosuchproject Whatever
  [ "$status" -ne 0 ]
  [[ "$output" == *"no project named"* ]]
}

@test "recall a stored scene" {
  run "${UCMIX_BIN}" recall "Main Live" "Opening Set"
  [ "$status" -eq 0 ]
  [[ "$output" == *"recalled Main Live / Opening Set"* ]]
}

@test "recall accepts slot names as well as titles" {
  run "${UCMIX_BIN}" recall "01.Main Live.proj" "01.Opening Set.scn"
  [ "$status" -eq 0 ]
  [[ "$output" == *"recalled"* ]]
}

@test "recall rejects an unknown scene" {
  run "${UCMIX_BIN}" recall "Main Live" nosuchscene
  [ "$status" -ne 0 ]
  [[ "$output" == *"no scene named"* ]]
}

@test "recall --json emits an envelope" {
  run "${UCMIX_BIN}" recall "Main Live" "Opening Set" --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"action\""* ]]
  [[ "$output" == *"recall"* ]]
}

@test "rename a scene and see the new title listed" {
  run "${UCMIX_BIN}" rename "Main Live" "Encore" "Encore Two"
  [ "$status" -eq 0 ]
  [[ "$output" == *"renamed Main Live / Encore to Encore Two"* ]]

  run "${UCMIX_BIN}" ls scenes "01.Main Live.proj"
  [ "$status" -eq 0 ]
  [[ "$output" == *"Encore Two"* ]]
}

@test "rename rejects an unknown scene" {
  run "${UCMIX_BIN}" rename "Main Live" nosuchscene Whatever
  [ "$status" -ne 0 ]
  [[ "$output" == *"no scene named"* ]]
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

@test "delete removes a scene and frees its slot" {
  run "${UCMIX_BIN}" delete "Main Live" "Encore" --yes
  [ "$status" -eq 0 ]
  [[ "$output" == *"deleted Main Live / Encore"* ]]
  [[ "$output" == *"02.Encore.scn"* ]]

  run "${UCMIX_BIN}" ls scenes "01.Main Live.proj"
  [ "$status" -eq 0 ]
  [[ "$output" != *"Encore"* ]]

  # The freed slot is reused by the next store.
  run "${UCMIX_BIN}" store "Main Live" "Reused"
  [ "$status" -eq 0 ]
  [[ "$output" == *"slot 02.Reused.scn"* ]]
}

@test "delete without --yes refuses in a non-tty" {
  run "${UCMIX_BIN}" delete "Main Live" "Encore" </dev/null
  [ "$status" -ne 0 ]
  [[ "$output" == *"destructive"* ]]
  [[ "$output" == *"--yes"* ]]
}

@test "delete rejects an unknown scene" {
  run "${UCMIX_BIN}" delete "Main Live" nosuchscene --yes
  [ "$status" -ne 0 ]
  [[ "$output" == *"no scene named"* ]]
}

@test "delete --json emits an envelope" {
  run "${UCMIX_BIN}" delete "Main Live" "Opening Set" --yes --json
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"action\""* ]]
  [[ "$output" == *"delete"* ]]
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
