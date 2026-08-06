#!/usr/bin/env bash

# Roll back to the binary and scheduling definitions kept by the last install or
# update. No build is needed: rollback restores what is already on disk, so this
# calls the installed binary rather than rebuilding the checkout.
#
# The SQLite database is never touched automatically — it is only restored from
# the backup directory by an operator who has confirmed corruption.
#
# Usage: scripts/deploy/rollback.sh

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALLED="${HOME}/.local/bin/jobfinder"
[[ -x "${INSTALLED}" ]] || INSTALLED="$(command -v jobfinder || true)"

if [[ -n "${INSTALLED}" && -x "${INSTALLED}" ]]; then
  exec "${INSTALLED}" rollback --assets "${PROJECT_ROOT}"
fi

printf 'no installed jobfinder found; run scripts/deploy/install.sh first\n' >&2
exit 1
