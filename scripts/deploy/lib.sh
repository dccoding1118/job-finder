#!/usr/bin/env bash
# Many names here are consumed only by the scripts that source this file, which
# the linter cannot see when it analyses lib.sh on its own.
# shellcheck disable=SC2034

# Shared library for the jobfinder single-user deployment scripts
# (install.sh / update.sh / rollback.sh). It resolves the XDG install
# locations, renders the systemd unit templates, and — most importantly —
# verifies effect at the running-process surface rather than at the install
# surface: "file copied" and "service active" are not proof that the version
# now running is the one just installed.

set -euo pipefail

DEPLOY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${DEPLOY_SCRIPT_DIR}/../.." && pwd)"

# --- install locations (XDG three-way split; §2 of docs/deploy.md) ----------
CONFIG_HOME="${XDG_CONFIG_HOME:-${HOME}/.config}"
DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}"

CONFIG_DIR="${CONFIG_HOME}/jobfinder"
DATA_DIR="${DATA_HOME}/jobfinder"
LIB_DIR="${HOME}/.local/lib/jobfinder"
UNIT_DIR="${CONFIG_HOME}/systemd/user"
BACKUP_DIR="${DATA_DIR}/backups"

BINARY="${LIB_DIR}/jobfinder"
PREV_BINARY="${LIB_DIR}/jobfinder.prev"
PREV_UNIT_DIR="${LIB_DIR}/units.prev"
CONFIG="${CONFIG_DIR}/config.yaml"
PROFILE="${CONFIG_DIR}/profile.yaml"
DENYLIST="${CONFIG_DIR}/pii-denylist.txt"
DB="${DATA_DIR}/jobs.db"
INSTALL_MANIFEST="${LIB_DIR}/manifest.txt"

API_SERVICE="jobfinder-api.service"
RUN_SERVICE="jobfinder-run.service"
RUN_TIMER="jobfinder-run.timer"
UNITS=("${API_SERVICE}" "${RUN_SERVICE}" "${RUN_TIMER}")

CONFIG_EXAMPLE="${PROJECT_ROOT}/configs/config.example.yaml"
PROFILE_EXAMPLE="${PROJECT_ROOT}/configs/profile.example.yaml"
UNIT_TEMPLATE_DIR="${PROJECT_ROOT}/deploy/production/systemd"

# --- output helpers ---------------------------------------------------------
log()  { printf '\033[1m==>\033[0m %s\n' "$*"; }
ok()   { printf '  \033[32mOK\033[0m   %s\n' "$*"; }
warn() { printf '  \033[33mWARN\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[31mdeploy failed:\033[0m %s\n' "$*" >&2; exit 1; }

# environment_blocked mirrors the verify harness contract: a missing user bus
# or toolchain is an environment problem, not a product defect, and exits 2 so
# callers can tell the two apart.
environment_blocked() {
  printf '\033[33menvironment blocked:\033[0m %s\n' "$*" >&2
  exit 2
}

need_cmd() { command -v "$1" >/dev/null 2>&1 || environment_blocked "missing required command: $1"; }

# --- systemd user session ---------------------------------------------------
require_user_bus() {
  need_cmd systemctl
  if [[ -z "${XDG_RUNTIME_DIR:-}" ]] || ! systemctl --user show-environment >/dev/null 2>&1; then
    environment_blocked "systemd --user bus unavailable (need a logged-in user session with linger)"
  fi
}

# require_linger keeps the timer and API service alive across logout, which the
# whole daily-fetch model depends on. It only reports; enabling linger needs
# root, so it is the operator's action.
require_linger() {
  local state
  state="$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null || printf 'no')"
  if [[ "${state}" != "yes" ]]; then
    warn "linger is not enabled; run: sudo loginctl enable-linger $(id -un)"
  else
    ok "linger enabled"
  fi
}

# --- config parsing ---------------------------------------------------------
# config_value <section> <key> reads a two-space-indented key from a top-level
# yaml section. Sufficient for the flat config.yaml; not a general parser.
config_value() {
  awk -v sec="$1:" -v key="  $2:" '
    $0 == sec { inside = 1; next }
    inside && /^[^[:space:]#]/ { inside = 0 }
    inside && index($0, key) == 1 { sub(key, ""); gsub(/^[[:space:]]+|[[:space:]]+$/, ""); print; exit }
  ' "${CONFIG}"
}

# --- unit rendering ---------------------------------------------------------
# render_unit substitutes the three jobfinder-owned paths into a copy of the
# template. The PATH= line keeps systemd's %h specifier (it points at
# ~/.local/bin and the mise shims, which live under $HOME regardless of XDG).
render_unit() {
  local src="$1" dest="$2"
  sed \
    -e "s|%h/.local/lib/jobfinder/jobfinder|${BINARY}|g" \
    -e "s|%h/.config/jobfinder/config.yaml|${CONFIG}|g" \
    -e "s|%h/.local/share/jobfinder|${DATA_DIR}|g" \
    "${src}" >"${dest}"
}

# install_units stashes the currently installed units for rollback, then
# renders fresh copies from the templates. It reports drift: a unit whose new
# render differs from the stashed one is either a template update or an operator
# edit about to be replaced — never silently swallowed.
install_units() {
  mkdir -p "${PREV_UNIT_DIR}"
  local unit rendered
  rendered="$(mktemp)"
  for unit in "${UNITS[@]}"; do
    if [[ -f "${UNIT_DIR}/${unit}" ]]; then
      cp "${UNIT_DIR}/${unit}" "${PREV_UNIT_DIR}/${unit}"
    fi
    render_unit "${UNIT_TEMPLATE_DIR}/${unit}" "${rendered}"
    if [[ -f "${UNIT_DIR}/${unit}" ]] && ! diff -q "${UNIT_DIR}/${unit}" "${rendered}" >/dev/null; then
      warn "unit ${unit} changed; replacing (previous kept in ${PREV_UNIT_DIR})"
      diff -u "${UNIT_DIR}/${unit}" "${rendered}" | sed 's/^/    /' >&2 || true
    fi
    install -m 0644 "${rendered}" "${UNIT_DIR}/${unit}"
  done
  rm -f "${rendered}"
  ok "installed units into ${UNIT_DIR}"
}

daemon_reload() { systemctl --user daemon-reload; }

# --- effect-surface verification -------------------------------------------
# assert_api_effective proves the running API process is the binary we just put
# on disk, and (when a restart marker is given) that it started after that
# marker — not a stale process still holding a since-replaced inode.
assert_api_effective() {
  local marker="${1:-0}"
  local state pid exe start_epoch
  state="$(systemctl --user show "${API_SERVICE}" -p ActiveState --value)"
  [[ "${state}" == "active" ]] || die "${API_SERVICE} is ${state}, want active"

  pid="$(systemctl --user show "${API_SERVICE}" -p MainPID --value)"
  [[ "${pid}" =~ ^[0-9]+$ && "${pid}" -gt 0 ]] || die "${API_SERVICE} has no MainPID"

  exe="$(readlink "/proc/${pid}/exe" 2>/dev/null || true)"
  if [[ "${exe}" != "${BINARY}" ]]; then
    die "running process (pid ${pid}) exec is '${exe}', want '${BINARY}' — a stale process is still live"
  fi

  if [[ "${marker}" -gt 0 ]]; then
    local start_ts
    start_ts="$(systemctl --user show "${API_SERVICE}" -p ExecMainStartTimestamp --value)"
    start_epoch="$(date -d "${start_ts}" +%s 2>/dev/null || printf '0')"
    if [[ "${start_epoch}" -lt "${marker}" ]]; then
      die "${API_SERVICE} did not restart (started ${start_ts}, before update marker)"
    fi
  fi
  ok "API service effective: pid ${pid} running ${BINARY}"
}

# assert_loopback_only fails if the API bound anything other than a loopback
# address on its port.
assert_loopback_only() {
  local addr port line
  addr="$(config_value api addr)"
  port="${addr##*:}"
  need_cmd ss
  line="$(ss -ltnH "sport = :${port}" 2>/dev/null || true)"
  [[ -n "${line}" ]] || die "nothing listening on port ${port}"
  if grep -Eqv '127\.0\.0\.1|\[::1\]' <<<"${line}"; then
    die "port ${port} has a non-loopback listener:\n${line}"
  fi
  ok "API listens on loopback only (:${port})"
}

# smoke_api reads the authenticated Job API from loopback and checks that an
# unauthenticated request is rejected.
smoke_api() {
  local addr token code
  addr="$(config_value api addr)"
  token="$(config_value api token)"
  need_cmd curl
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 10 \
    -H "Authorization: Bearer ${token}" "http://${addr}/api/v1/jobs" || true)"
  [[ "${code}" == "200" ]] || die "authenticated GET /api/v1/jobs returned ${code}, want 200"
  code="$(curl -s -o /dev/null -w '%{http_code}' -m 10 "http://${addr}/api/v1/jobs" || true)"
  [[ "${code}" == "401" ]] || die "unauthenticated GET /api/v1/jobs returned ${code}, want 401"
  ok "API smoke passed (authenticated 200, unauthenticated 401)"
}

# assert_run_armed proves the scheduled fetch is wired without fetching: the
# one-shot unit loads cleanly and the timer is active with a future trigger.
# Deployment never performs a fetch — the operator triggers one on demand or
# waits for the timer.
assert_run_armed() {
  local load state next
  load="$(systemctl --user show "${RUN_SERVICE}" -p LoadState --value)"
  [[ "${load}" == "loaded" ]] || die "${RUN_SERVICE} LoadState=${load}, want loaded"
  state="$(systemctl --user show "${RUN_TIMER}" -p ActiveState --value)"
  [[ "${state}" == "active" ]] || die "${RUN_TIMER} ActiveState=${state}, want active"
  next="$(systemctl --user show "${RUN_TIMER}" -p NextElapseUSecRealtime --value)"
  [[ -n "${next}" && "${next}" != "0" && "${next}" != "n/a" ]] \
    || die "${RUN_TIMER} has no scheduled next trigger"
  ok "fetch is armed: ${RUN_SERVICE} loaded, ${RUN_TIMER} active (next ${next})"
}

# write_manifest records what was installed so an operator can prove the running
# binary's provenance later.
write_manifest() {
  local action="$1" revision dirty
  revision="$(git -C "${PROJECT_ROOT}" rev-parse --verify HEAD 2>/dev/null || printf 'uncommitted')"
  dirty=false
  [[ -n "$(git -C "${PROJECT_ROOT}" status --porcelain 2>/dev/null)" ]] && dirty=true
  {
    printf 'action=%s\n' "${action}"
    printf 'installed_at=%s\n' "$(TZ=Asia/Taipei date --iso-8601=seconds)"
    printf 'revision=%s\n' "${revision}"
    printf 'dirty=%s\n' "${dirty}"
    printf 'binary_sha256=%s\n' "$(sha256sum "${BINARY}" | awk '{print $1}')"
    printf 'config=%s\n' "${CONFIG}"
    printf 'db=%s\n' "${DB}"
  } >"${INSTALL_MANIFEST}"
  chmod 600 "${INSTALL_MANIFEST}"
}

now_epoch() { date +%s; }
