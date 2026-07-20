#!/usr/bin/env bash

# Update an existing jobfinder install to the current checkout: rebuild, replace
# the binary and units, keep the previous binary for rollback, and restart the
# API service with try-restart (enable --now is a no-op on an already-running
# service). Config, profile, denylist and database are left untouched.
#
# Usage: scripts/deploy/update.sh [--skip-tests]

set -euo pipefail
# shellcheck source=scripts/deploy/lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

skip_tests=false
[[ "${1:-}" == "--skip-tests" ]] && skip_tests=true

need_cmd mise
need_cmd sha256sum
require_user_bus
[[ -f "${BINARY}" ]] || die "no existing install at ${BINARY}; run install.sh first"

log "preflight: build and unit tests"
( cd "${PROJECT_ROOT}" && mise run fmt && mise run lint )
if [[ "${skip_tests}" == false ]]; then
  ( cd "${PROJECT_ROOT}" && mise run test )
else
  warn "--skip-tests: unit tests skipped"
fi
( cd "${PROJECT_ROOT}" && mise run build )
BUILT_BINARY="${PROJECT_ROOT}/bin/jobfinder"
[[ -f "${BUILT_BINARY}" ]] || die "built binary missing at ${BUILT_BINARY}"

log "replace binary and units"
cp "${BINARY}" "${PREV_BINARY}"
install -m 0755 "${BUILT_BINARY}" "${BINARY}"
ok "installed ${BINARY} (previous kept at ${PREV_BINARY})"
install_units
daemon_reload

log "restart API service and smoke"
marker="$(now_epoch)"
systemctl --user try-restart "${API_SERVICE}"
for _ in $(seq 1 20); do
  [[ "$(systemctl --user show "${API_SERVICE}" -p ActiveState --value)" == "active" ]] && break
  sleep 0.5
done
assert_api_effective "${marker}"
assert_loopback_only
smoke_api

write_manifest update
log "update complete"
