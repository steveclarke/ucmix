#!/usr/bin/env bats
# End-to-end coverage for the board-as-code commands: apply, verify, and
# dump --as-config. The fakeboard runs with the ×100 volume read-scale enabled
# (FAKEBOARD_VOLUME_READ_SCALE), faithful to the real board, so the round-trip
# exercises the read-scale seam: apply writes the human dB via Set (taper only,
# no read-scale) and verify diffs the read-scale snapshot back.

load test_helper

CFG="${UCMIX_ROOT}/testdata/board-as-code.yml"

# Override the shared setup to turn on the read-scale quirk before the fakeboard
# starts; everything else matches test_helper's setup.
setup() {
  export NO_COLOR=1
  export FAKEBOARD_VOLUME_READ_SCALE=1
  TEST_TMP="$(mktemp -d)"
  cd "${TEST_TMP}" || exit 1
  start_fakeboard
}

@test "apply then verify round-trips clean (the read-scale seam)" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]
  [[ "$output" == *"verify clean"* ]]

  run "${UCMIX_BIN}" verify "${CFG}"
  [ "$status" -eq 0 ]
  [[ "$output" == *"clean"* ]]
}

@test "dump --as-config round-trips: apply, dump, verify the dump" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  "${UCMIX_BIN}" dump --as-config >out.yml
  [ -s out.yml ]
  # It is a declarative config, not a raw dump.
  grep -q "channels:" out.yml
  grep -q "fader: -6" out.yml

  run "${UCMIX_BIN}" verify out.yml
  [ "$status" -eq 0 ]
  [[ "$output" == *"clean"* ]]
}

@test "verify on a drifted board exits 1 and lists drift" {
  # Seed board has never had the config applied → declared paths differ.
  run "${UCMIX_BIN}" verify "${CFG}"
  [ "$status" -eq 1 ]
  [[ "$output" == *"drift"* ]]
}

@test "verify --json on a drifted board exits 1 with valid JSON" {
  run "${UCMIX_BIN}" --json verify "${CFG}"
  [ "$status" -eq 1 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"clean\":false"* ]] || [[ "$output" == *"\"clean\": false"* ]]
}

@test "verify --json emits a machine-readable result" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  run "${UCMIX_BIN}" --json verify "${CFG}"
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"clean\""* ]]
}

@test "apply --dry-run prints the plan and never connects" {
  # Point at a dead address: dry-run must not dial, so it still exits 0.
  UCMIX_HOST=127.0.0.1:1 run "${UCMIX_BIN}" apply --dry-run "${CFG}"
  [ "$status" -eq 0 ]
  [[ "$output" == *"write plan"* ]]
  [[ "$output" == *"line/ch1/volume"* ]]
}

@test "apply --dry-run --json lists the ordered plan" {
  UCMIX_HOST=127.0.0.1:1 run "${UCMIX_BIN}" --json apply --dry-run "${CFG}"
  [ "$status" -eq 0 ]
  echo "$output" | json_valid
  [[ "$output" == *"\"plan\""* ]]
  [[ "$output" == *"line/ch1/volume"* ]]
}

@test "apply --reset --yes resets, applies, and verifies clean" {
  run "${UCMIX_BIN}" apply --reset --yes "${CFG}"
  [ "$status" -eq 0 ]
  [[ "$output" == *"verify clean"* ]]
}

@test "apply --reset without --yes refuses in a non-tty" {
  run "${UCMIX_BIN}" apply --reset "${CFG}"
  [ "$status" -eq 2 ]
  [[ "$output" == *"not confirmed"* ]]
}

@test "verify on a missing config file exits 2" {
  run "${UCMIX_BIN}" verify /no/such/config.yml
  [ "$status" -eq 2 ]
  [[ "$output" == *"could not read config"* ]]
}
