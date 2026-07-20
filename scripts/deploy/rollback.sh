#!/usr/bin/env bash

# Roll back the last binary/unit update: restore the previous binary and units
# kept by install.sh/update.sh, then try-restart the API service and verify the
# running process is the restored version. The SQLite database is never touched
# automatically — it is only restored from BACKUP_DIR by an operator when data
# corruption is confirmed.
#
# Usage: scripts/deploy/rollback.sh

set -euo pipefail
# shellcheck source=scripts/deploy/lib.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib.sh"

require_user_bus
[[ -f "${PREV_BINARY}" ]] || die "no previous binary at ${PREV_BINARY}; nothing to roll back to"

log "restore previous binary and units"
# Keep the current (bad) binary aside so a forward roll is still possible.
[[ -f "${BINARY}" ]] && cp "${BINARY}" "${LIB_DIR}/jobfinder.bad"
install -m 0755 "${PREV_BINARY}" "${BINARY}"
ok "restored ${BINARY} from ${PREV_BINARY}"

if [[ -d "${PREV_UNIT_DIR}" ]]; then
  for unit in "${UNITS[@]}"; do
    if [[ -f "${PREV_UNIT_DIR}/${unit}" ]]; then
      install -m 0644 "${PREV_UNIT_DIR}/${unit}" "${UNIT_DIR}/${unit}"
    fi
  done
  ok "restored units from ${PREV_UNIT_DIR}"
else
  warn "no stashed units at ${PREV_UNIT_DIR}; units left as-is"
fi
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

write_manifest rollback
log "rollback complete"
printf '\nData note: SQLite at %s was not touched.\n' "${DB}"
printf 'Restore it from %s only if corruption is confirmed.\n' "${BACKUP_DIR}"
