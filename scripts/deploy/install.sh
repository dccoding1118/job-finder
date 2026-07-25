#!/usr/bin/env bash

# Fresh install of jobfinder for the single-user MVP: build a verified binary,
# provision the XDG config/data locations, install and enable the systemd user
# units, then verify the running service on the effect surface. Existing config,
# profile, denylist and database are never overwritten.
#
# Usage: scripts/deploy/install.sh [--skip-tests]
# Not part of the verify harness and never invoked by any e2e-* task.

set -euo pipefail
# shellcheck source=scripts/deploy/lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

skip_tests=false
[[ "${1:-}" == "--skip-tests" ]] && skip_tests=true

need_cmd mise
need_cmd sha256sum
require_user_bus
require_file_exists() { [[ -f "$1" ]] || die "missing $2 at $1"; }
require_file_exists "${CONFIG_EXAMPLE}" "config example"
require_file_exists "${PROFILE_EXAMPLE}" "profile example"

# --- 1. preflight: program-level gate + verified build ----------------------
log "preflight: build and unit tests"
( cd "${PROJECT_ROOT}" && mise run fmt && mise run lint )
if [[ "${skip_tests}" == false ]]; then
  ( cd "${PROJECT_ROOT}" && mise run test )
else
  warn "--skip-tests: unit tests skipped"
fi
( cd "${PROJECT_ROOT}" && mise run build )
BUILT_BINARY="${PROJECT_ROOT}/bin/jobfinder"
require_file_exists "${BUILT_BINARY}" "built binary"
ok "built ${BUILT_BINARY}"

# --- 2. config and data directories -----------------------------------------
log "provision directories"
mkdir -p "${CONFIG_DIR}" "${DATA_DIR}" "${LIB_DIR}" "${UNIT_DIR}" "${BACKUP_DIR}"
chmod 700 "${CONFIG_DIR}" "${DATA_DIR}" "${BACKUP_DIR}"
chmod 755 "${LIB_DIR}"
require_linger

# config: render on first install; never overwrite an existing one.
if [[ -f "${CONFIG}" ]]; then
  ok "config exists, kept: ${CONFIG}"
else
  need_cmd openssl
  token="$(openssl rand -hex 24)"
  sed \
    -e "s|^  path: \.local-dev/jobfinder\.db\$|  path: ${DB}|" \
    -e "s|^  path: \.local-dev/profile\.yaml\$|  path: ${PROFILE}|" \
    -e "s|^  denylist: \.local-dev/pii-denylist\.txt\$|  denylist: ${DENYLIST}|" \
    -e "s|^  token: CHANGE_ME\$|  token: ${token}|" \
    "${CONFIG_EXAMPLE}" >"${CONFIG}"
  chmod 600 "${CONFIG}"
  ok "rendered config with generated token: ${CONFIG}"
  warn "set api.extension_origin to the real chrome-extension:// id before loading the extension"
fi
chmod 600 "${CONFIG}"

# profile and denylist: seed from the anonymised example only if absent.
if [[ -f "${PROFILE}" ]]; then
  ok "profile exists, kept: ${PROFILE}"
else
  install -m 0600 "${PROFILE_EXAMPLE}" "${PROFILE}"
  warn "seeded anonymised example profile; replace ${PROFILE} with your own"
fi
if [[ ! -f "${DENYLIST}" ]]; then
  printf '# jobfinder PII denylist: one forbidden marker per line (kept out of version control)\n' >"${DENYLIST}"
  chmod 600 "${DENYLIST}"
  warn "created empty denylist; add your PII markers to ${DENYLIST}"
fi
chmod 600 "${DENYLIST}"

# profile lint gate before the binary is placed.
log "profile lint gate"
"${BUILT_BINARY}" profile lint --profile "${PROFILE}" --denylist "${DENYLIST}"

# --- 3. install binary and units --------------------------------------------
log "install binary and units"
[[ -f "${BINARY}" ]] && cp "${BINARY}" "${PREV_BINARY}"
install -m 0755 "${BUILT_BINARY}" "${BINARY}"
ok "installed ${BINARY}"
install_units
daemon_reload
systemctl --user enable "${API_SERVICE}" "${RUN_TIMER}" >/dev/null

# --- 4. start and smoke ------------------------------------------------------
log "start services and smoke"
marker="$(now_epoch)"
systemctl --user restart "${API_SERVICE}"
systemctl --user start "${RUN_TIMER}"
for _ in $(seq 1 20); do
  [[ "$(systemctl --user show "${API_SERVICE}" -p ActiveState --value)" == "active" ]] && break
  sleep 0.5
done
assert_api_effective "${marker}"
assert_loopback_only
smoke_api

log "check the scheduled fetch is armed"
assert_run_armed

write_manifest install
log "install complete"
printf '\nInstalled:\n  binary : %s\n  config : %s\n  data   : %s\n  units  : %s\n' \
  "${BINARY}" "${CONFIG}" "${DATA_DIR}" "${UNIT_DIR}"
printf '\nThe API service and the daily fetch timer are enabled. No fetch runs at install time.\n'
printf 'Fetch on demand: systemctl --user start %s\n' "${RUN_SERVICE}"
printf 'Diagnostics: journalctl --user -u %s\n' "${API_SERVICE}"
