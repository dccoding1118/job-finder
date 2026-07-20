#!/usr/bin/env bash

set -euo pipefail

VERIFY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${VERIFY_SCRIPT_DIR}/../.." && pwd)"
VERIFY_ROOT="${VERIFY_ROOT:-${PROJECT_ROOT}/.local-dev/verify}"
ARTIFACT_ROOT="${VERIFY_ROOT}/artifact"
HARNESS_ROOT="${VERIFY_ROOT}/harness"
RUNTIME_ROOT="${VERIFY_ROOT}/runtime"
EVIDENCE_ROOT="${VERIFY_ROOT}/evidence"
VERIFY_BINARY="${ARTIFACT_ROOT}/bin/jobfinder"
VERIFY_PROFILE="${RUNTIME_ROOT}/profile.yaml"
VERIFY_DENYLIST="${RUNTIME_ROOT}/pii-denylist.txt"
MOCK_CONFIG="${RUNTIME_ROOT}/config-mock.yaml"
LIVE_CONFIG="${RUNTIME_ROOT}/config-live.yaml"
MOCK_DB="${RUNTIME_ROOT}/mock.db"
LIVE_DB="${RUNTIME_ROOT}/live.db"
ARTIFACT_MANIFEST="${ARTIFACT_ROOT}/manifest.txt"

require_file() {
  local path="$1"
  local description="$2"

  if [[ ! -f "${path}" ]]; then
    printf 'environment blocked: missing %s at %s\n' "${description}" "${path}" >&2
    exit 2
  fi
}

timestamp() {
  TZ=Asia/Taipei date '+%Y%m%d-%H%M%S%z'
}

require_mode() {
  local path="$1"
  local expected="$2"
  local description="$3"
  local actual

  actual="$(stat -c '%a' "${path}")"
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'verification failed: %s mode is %s, want %s\n' "${description}" "${actual}" "${expected}" >&2
    exit 1
  fi
}

# Reporting and environment primitives shared by run-mock.sh and run-live.sh.
# They read the caller's runbook globals (report, current_step, current_title,
# pass_count); shellcheck cannot see those assignments when it analyses lib.sh in
# isolation, so SC2154 is suppressed here rather than at every call site.

# shellcheck disable=SC2154
record() {
  printf '%s\n' "$1" >>"${report}"
}

# shellcheck disable=SC2154
pass_step() {
  record '- 狀態：PASS'
  pass_count=$(( ${pass_count:-0} + 1 ))
}

# shellcheck disable=SC2154
fail() {
  record '- 狀態：FAIL'
  record "- 原因：$1"
  record ''
  record '## 結果'
  record '- 結果：FAIL'
  printf 'verification failed at step %s (%s): %s\n' "${current_step:-preflight}" "${current_title:-environment preflight}" "$1" >&2
  exit 1
}

# shellcheck disable=SC2154
environment_blocked() {
  if [[ -n "${current_step:-}" ]]; then
    record '- 狀態：ENVIRONMENT_BLOCKED'
  fi
  record "- 環境阻塞：$1"
  record ''
  record '## 結果'
  record '- 結果：ENVIRONMENT_BLOCKED'
  printf 'environment blocked at step %s (%s): %s\n' "${current_step:-preflight}" "${current_title:-environment preflight}" "$1" >&2
  exit 2
}

# require_commands blocks the run when any named executable is absent.
require_commands() {
  local command_name
  for command_name in "$@"; do
    command -v "${command_name}" >/dev/null || environment_blocked "missing required command: ${command_name}"
  done
}

# stop_transient_units tears down any transient API/timer units a runbook started
# so they never linger after the trap fires; unset unit names are skipped.
stop_transient_units() {
  if [[ -n "${api_unit:-}" ]]; then
    systemctl --user stop "${api_unit}.service" >/dev/null 2>&1 || true
    systemctl --user reset-failed "${api_unit}.service" >/dev/null 2>&1 || true
  fi
  if [[ -n "${timer_unit:-}" ]]; then
    systemctl --user stop "${timer_unit}.timer" "${timer_unit}.service" >/dev/null 2>&1 || true
    systemctl --user reset-failed "${timer_unit}.timer" "${timer_unit}.service" >/dev/null 2>&1 || true
  fi
}

# assert_profile_perms enforces the isolation contract on the verification root
# and the anonymised Profile, denylist and the given config.
assert_profile_perms() {
  require_mode "${VERIFY_ROOT}" 700 'verification root'
  require_mode "${VERIFY_PROFILE}" 600 'verification profile'
  require_mode "${VERIFY_DENYLIST}" 600 'verification denylist'
  require_mode "$1" 600 'verification config'
}
