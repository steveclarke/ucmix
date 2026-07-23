#!/usr/bin/env bats
# End-to-end coverage for connection and host-resolution error paths.

load test_helper

@test "an unreachable host fails with a connect hint" {
  run "${UCMIX_BIN}" --host 127.0.0.1:1 get line.ch1.mute
  [ "$status" -ne 0 ]
  [[ "$output" == *"could not connect"* ]]
  [[ "$output" == *"UCMIX_HOST"* ]]
}

@test "no configured host fails with a hint" {
  unset UCMIX_HOST
  # Point config resolution at an empty dir so no config file is found.
  run env -u UCMIX_HOST HOME="${TEST_TMP}" XDG_CONFIG_HOME="${TEST_TMP}/empty" "${UCMIX_BIN}" get line.ch1.mute
  [ "$status" -ne 0 ]
  [[ "$output" == *"no mixer host configured"* ]]
}

@test "an unknown command fails" {
  run "${UCMIX_BIN}" frobnicate
  [ "$status" -ne 0 ]
}
