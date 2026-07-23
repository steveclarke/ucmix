#!/usr/bin/env bats
# Smoke test — proves the built binary runs. Real command coverage arrives with
# the CLI in Phase 6.

BIN="${BATS_TEST_DIRNAME}/../dist/ucmix"

@test "ucmix --version runs" {
  run "$BIN" --version
  [ "$status" -eq 0 ]
  [[ "$output" == ucmix* ]]
}
