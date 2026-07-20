#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
source "${SCRIPT_DIR}/lib.sh"

mkdir -p "${VERIFY_ROOT}" "${EVIDENCE_ROOT}"
find "${VERIFY_ROOT}" -mindepth 1 -maxdepth 1 ! -name evidence -exec rm -rf {} +
mkdir -p "${RUNTIME_ROOT}/tmp" "${EVIDENCE_ROOT}"
chmod 700 "${VERIFY_ROOT}" "${RUNTIME_ROOT}" "${RUNTIME_ROOT}/tmp" "${EVIDENCE_ROOT}"

printf 'reset verification artifact, harness, and runtime at %s\n' "${VERIFY_ROOT}"
