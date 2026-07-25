#!/usr/bin/env bats
# End-to-end coverage for the board-as-code commands: apply, verify, and
# dump --as-config. apply writes the human dB via Set (taper → plain 0..1 wire)
# and verify diffs the plain-wire snapshot back — the same 0..1 scale a real 32R
# returns on read.

load test_helper

CFG="${UCMIX_ROOT}/testdata/board-as-code.yml"

@test "apply then verify round-trips clean" {
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

@test "color verifies clean however it is written (issue #30)" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  # The config declares 6 digits; the board reports the packed integer. Every
  # spelling of the same color must verify clean.
  for spelling in 8f96a3 8f96a3ff "#8f96a3" 8F96A3FF; do
    cat >color.yml <<YML
version: 1
channels:
  1:
    color: "${spelling}"
YML
    run "${UCMIX_BIN}" verify color.yml
    [ "$status" -eq 0 ]
    [[ "$output" == *"clean"* ]]
  done
}

@test "a genuinely different color drifts, printed as canonical hex" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  cat >color.yml <<'YML'
version: 1
channels:
  1:
    color: "ff0000"
YML
  run "${UCMIX_BIN}" verify color.yml
  [ "$status" -eq 1 ]
  [[ "$output" == *"ff0000ff"* ]]
  [[ "$output" == *"8f96a3ff"* ]]
}

@test "get and verify render a color the same way" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  run "${UCMIX_BIN}" get line/ch1/color
  [ "$status" -eq 0 ]
  [[ "$output" == *"8f96a3ff"* ]]

  # --raw once disagreed with get, reporting the packed integer (issue #30).
  run "${UCMIX_BIN}" get line/ch1/color --raw
  [ "$status" -eq 0 ]
  [[ "$output" == *"8f96a3ff"* ]]
  [[ "$output" != *"4288910991"* ]]

  run "${UCMIX_BIN}" dump line/ch1/color
  [ "$status" -eq 0 ]
  [[ "$output" == *"8f96a3ff"* ]]
}

@test "dump --as-config carries the color through a verify" {
  run "${UCMIX_BIN}" apply "${CFG}"
  [ "$status" -eq 0 ]

  "${UCMIX_BIN}" dump --as-config >out.yml
  grep -q "color: 8f96a3ff" out.yml

  run "${UCMIX_BIN}" verify out.yml
  [ "$status" -eq 0 ]
  [[ "$output" == *"clean"* ]]
}
