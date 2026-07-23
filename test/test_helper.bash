#!/usr/bin/env bash
# Shared setup for the ucmix end-to-end suite. Each test boots its own fakeboard
# on an ephemeral port, points UCMIX_HOST at it, and runs in a fresh temp dir.
# `just test-e2e` builds dist/ucmix and dist/fakeboard before invoking bats.

UCMIX_ROOT="$(cd "${BATS_TEST_DIRNAME}/.." && pwd)"
UCMIX_BIN="${UCMIX_ROOT}/dist/ucmix"
FAKEBOARD_BIN="${UCMIX_ROOT}/dist/fakeboard"
SEED_FIXTURE="${UCMIX_ROOT}/testdata/seed.json"

# start_fakeboard launches a fakeboard seeded from the fixture and exports
# UCMIX_HOST with the address it prints. Sets FAKE_PID and FAKE_DIR.
start_fakeboard() {
  if [ ! -x "${FAKEBOARD_BIN}" ]; then
    echo "fakeboard binary not found at ${FAKEBOARD_BIN} — run 'just test-e2e' (it builds it)" >&2
    return 1
  fi
  FAKE_DIR="$(mktemp -d)"
  local addr_file="${FAKE_DIR}/addr"
  "${FAKEBOARD_BIN}" --seed "${SEED_FIXTURE}" >"${addr_file}" 2>"${FAKE_DIR}/err" &
  FAKE_PID=$!

  local tries=0
  while [ ! -s "${addr_file}" ] && [ "${tries}" -lt 100 ]; do
    sleep 0.05
    tries=$((tries + 1))
  done
  UCMIX_HOST="$(head -n1 "${addr_file}")"
  export UCMIX_HOST
  if [ -z "${UCMIX_HOST}" ]; then
    echo "fakeboard did not report an address; stderr:" >&2
    cat "${FAKE_DIR}/err" >&2
    return 1
  fi
}

# stop_fakeboard kills the fakeboard and cleans its temp dir.
stop_fakeboard() {
  if [ -n "${FAKE_PID:-}" ]; then
    kill "${FAKE_PID}" 2>/dev/null || true
    wait "${FAKE_PID}" 2>/dev/null || true
  fi
  [ -n "${FAKE_DIR:-}" ] && rm -rf "${FAKE_DIR}"
}

setup() {
  # Keep output free of ANSI codes so assertions match plain text.
  export NO_COLOR=1
  TEST_TMP="$(mktemp -d)"
  cd "${TEST_TMP}" || exit 1
  start_fakeboard
}

teardown() {
  stop_fakeboard
  cd "${UCMIX_ROOT}" || true
  [ -n "${TEST_TMP:-}" ] && rm -rf "${TEST_TMP}"
}

# json_valid checks that its stdin parses as JSON (jq if present, else python3).
json_valid() {
  if command -v jq >/dev/null 2>&1; then
    jq -e . >/dev/null
  else
    python3 -m json.tool >/dev/null
  fi
}
