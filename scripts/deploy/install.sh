#!/usr/bin/env bash

# Deploy the current development checkout.
#
# Installation itself is `jobfinder install`; this wrapper only produces the
# binary to install. It runs the program-level gate (fmt, lint, test), builds,
# and hands the freshly built executable the checkout as its asset directory —
# the same subcommand a release artifact runs, so what is exercised here is what
# a user gets.
#
# Usage: scripts/deploy/install.sh [--skip-tests] [extra jobfinder install flags]
# Not part of the verify harness and never invoked by any e2e-* task.

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ACTION="${JOBFINDER_DEPLOY_ACTION:-install}"

skip_tests=false
if [[ "${1:-}" == "--skip-tests" ]]; then
  skip_tests=true
  shift
fi

command -v mise >/dev/null 2>&1 || { printf 'missing required command: mise\n' >&2; exit 2; }

printf '\033[1m==>\033[0m preflight: format, lint%s\n' "$([[ "${skip_tests}" == true ]] && printf '' || printf ', unit tests')"
(
  cd "${PROJECT_ROOT}"
  mise run fmt
  mise run lint
  [[ "${skip_tests}" == true ]] || mise run test
  mise run build
)

BUILT="${PROJECT_ROOT}/bin/jobfinder"
[[ -x "${BUILT}" ]] || { printf 'built binary missing at %s\n' "${BUILT}" >&2; exit 1; }

exec "${BUILT}" "${ACTION}" --assets "${PROJECT_ROOT}" "$@"
