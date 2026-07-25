#!/usr/bin/env bats
# End-to-end coverage for connection and host-resolution error paths.

load test_helper

@test "an unreachable host fails with a connect hint" {
  run "${UCMIX_BIN}" --host 127.0.0.1:1 get line.ch1.mute
  [ "$status" -ne 0 ]
  [[ "$output" == *"could not connect"* ]]
  [[ "$output" == *"reachable"* ]]
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

@test "ls scenes without a project names the missing argument" {
  run "${UCMIX_BIN}" ls scenes
  [ "$status" -ne 0 ]
  [[ "$output" == *"ls scenes needs a project name"* ]]
  [[ "$output" == *"ucmix ls projects"* ]]
  # Cobra's bare count message is gone.
  [[ "$output" != *"accepts 1 arg(s)"* ]]
}

@test "a missing argument names what the command wants" {
  run "${UCMIX_BIN}" get
  [ "$status" -ne 0 ]
  [[ "$output" == *"get needs a path"* ]]

  run "${UCMIX_BIN}" recall "Main Live"
  [ "$status" -ne 0 ]
  [[ "$output" == *"recall needs a project and a scene"* ]]
}

@test "a command with no better hint falls back to its usage line" {
  run "${UCMIX_BIN}" verify
  [ "$status" -ne 0 ]
  [[ "$output" == *"verify needs a config file"* ]]
  [[ "$output" == *"usage: ucmix verify <config.yml>"* ]]
}
