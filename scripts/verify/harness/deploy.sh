#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib.sh
source "${SCRIPT_DIR}/../lib.sh"

profile_source="${VERIFY_PROFILE_SOURCE:-${PROJECT_ROOT}/scripts/verify/fixtures/profile.synthetic.yaml}"
denylist_source="${VERIFY_DENYLIST_SOURCE:-}"
mock_config_source="${VERIFY_MOCK_CONFIG_SOURCE:-${PROJECT_ROOT}/configs/verify.mock.yaml}"

require_file "${profile_source}" "verification profile source"
require_file "${mock_config_source}" "mock verification config source"
command -v sha256sum >/dev/null || {
  printf 'environment blocked: sha256sum is required\n' >&2
  exit 2
}

mkdir -p "${ARTIFACT_ROOT}/bin" "${ARTIFACT_ROOT}/extension" "${ARTIFACT_ROOT}/systemd" "${ARTIFACT_ROOT}/fixtures" \
  "${HARNESS_ROOT}/bin" "${RUNTIME_ROOT}/tmp" "${RUNTIME_ROOT}/systemd-mock" "${EVIDENCE_ROOT}"
chmod 700 "${VERIFY_ROOT}" "${ARTIFACT_ROOT}" "${HARNESS_ROOT}" "${RUNTIME_ROOT}" "${RUNTIME_ROOT}/tmp" "${EVIDENCE_ROOT}"

(
  cd "${PROJECT_ROOT}"
  mise run build
)

install -m 0755 "${PROJECT_ROOT}/bin/jobfinder" "${VERIFY_BINARY}"
install -m 0755 "${PROJECT_ROOT}/scripts/verify/harness/fake-agent.sh" "${HARNESS_ROOT}/bin/claude"
install -m 0755 "${PROJECT_ROOT}/scripts/verify/harness/fake-agent.sh" "${HARNESS_ROOT}/bin/codex"
install -m 0600 "${profile_source}" "${VERIFY_PROFILE}"
install -m 0600 "${PROJECT_ROOT}/scripts/verify/fixtures/profile.synthetic.json" "${VERIFY_PROFILE_JSON}"
sed "s|__VERIFY_ROOT__|${VERIFY_ROOT}|g" "${mock_config_source}" >"${MOCK_CONFIG}"
chmod 600 "${MOCK_CONFIG}"

if [[ -n "${denylist_source}" ]]; then
  require_file "${denylist_source}" "verification denylist source"
  install -m 0600 "${denylist_source}" "${VERIFY_DENYLIST}"
else
  printf 'FORBIDDEN_MARKER\n' >"${VERIFY_DENYLIST}"
  chmod 600 "${VERIFY_DENYLIST}"
fi

cp -R "${PROJECT_ROOT}/extension/." "${ARTIFACT_ROOT}/extension/"
find "${ARTIFACT_ROOT}/extension" -type d -exec chmod 755 {} +
find "${ARTIFACT_ROOT}/extension" -type f -exec chmod 644 {} +
require_file "${ARTIFACT_ROOT}/extension/manifest.json" "extension manifest"

for unit in jobfinder-api.service jobfinder-run.service jobfinder-run.timer; do
  install -m 0644 "${PROJECT_ROOT}/deploy/production/systemd/${unit}" "${ARTIFACT_ROOT}/systemd/${unit}"
  sed \
    -e "s|%h/.local/lib/jobfinder/jobfinder|${VERIFY_BINARY}|g" \
    -e "s|%h/.config/jobfinder/config.yaml|${MOCK_CONFIG}|g" \
    -e "s|%h/.local/share/jobfinder|${RUNTIME_ROOT}|g" \
    -e "s|^Environment=PATH=.*|Environment=PATH=${HARNESS_ROOT}/bin:/usr/local/bin:/usr/bin:/bin|" \
    "${ARTIFACT_ROOT}/systemd/${unit}" >"${RUNTIME_ROOT}/systemd-mock/${unit}"
done
chmod 644 "${RUNTIME_ROOT}/systemd-mock/"*

revision="$(git -C "${PROJECT_ROOT}" rev-parse --verify HEAD 2>/dev/null || printf 'uncommitted')"
build_time="$(TZ=Asia/Taipei date --iso-8601=seconds)"
dirty=false
if [[ -n "$(git -C "${PROJECT_ROOT}" status --porcelain 2>/dev/null)" ]]; then dirty=true; fi
checksum="$(sha256sum "${VERIFY_BINARY}" | awk '{print $1}')"

{
  printf 'revision=%s\n' "${revision}"
  printf 'dirty=%s\n' "${dirty}"
  printf 'built_at=%s\n' "${build_time}"
  printf 'binary_sha256=%s\n' "${checksum}"
  printf 'profile_source=%s\n' "$(basename "${profile_source}")"
  printf 'mock_config_template_sha256=%s\n' "$(sha256sum "${mock_config_source}" | awk '{print $1}')"
  printf 'extension_manifest_sha256=%s\n' "$(sha256sum "${ARTIFACT_ROOT}/extension/manifest.json" | awk '{print $1}')"
  printf 'run_unit_template_sha256=%s\n' "$(sha256sum "${ARTIFACT_ROOT}/systemd/jobfinder-run.service" | awk '{print $1}')"
} >"${ARTIFACT_MANIFEST}"
chmod 600 "${ARTIFACT_MANIFEST}"

"${VERIFY_BINARY}" --help >/dev/null

printf 'deployed verification artifact to %s\n' "${VERIFY_ROOT}"
