#!/usr/bin/env bash

# Build the release artifacts for one version string.
#
# Two callers share this script: .github/workflows/release.yml passes a tag and
# every supported target, and `mise run pack` passes `dev` and the targets being
# tested. Sharing it is what makes "the dev deployment package has the same
# shape as a release artifact" a mechanical property rather than something
# people have to keep in sync by hand.
#
# Usage: pack.sh --version <ver> [--targets os/arch,...] [--out <dir>]
#   --version  version string; a vX.Y.Z tag, or `dev` for a deployment package
#   --targets  comma-separated GOOS/GOARCH list (default: every release target)
#   --out      output directory, created if absent (default: dist)

set -euo pipefail

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION=""
TARGETS="linux/amd64,linux/arm64,darwin/arm64,darwin/amd64,windows/amd64"
OUT="dist"

die() { printf '\033[31mpack failed:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1m==>\033[0m %s\n' "$*"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --targets) TARGETS="${2:-}"; shift 2 ;;
    --out)     OUT="${2:-}"; shift 2 ;;
    -h|--help) sed -n '3,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ -n "${VERSION}" ]] || die "--version is required"
command -v python3 >/dev/null 2>&1 || die "missing required command: python3"

# The manifest only accepts a numeric version, so a tag contributes its semver
# and every other version string maps to 0.0.0 — a value no release can claim.
if [[ "${VERSION}" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]]; then
  MANIFEST_VERSION="${BASH_REMATCH[1]}"
else
  MANIFEST_VERSION="0.0.0"
fi

# A tag is stamped into the binary; `dev` deliberately is not. Leaving the
# symbol empty lets internal/version fall back to the VCS revision, so a
# deployment package identifies itself as `dev (<commit>)` and cannot be
# mistaken for a release.
LDFLAGS="-s -w"
if [[ "${VERSION}" != dev ]]; then
  LDFLAGS="${LDFLAGS} -X github.com/dccoding1118/job-finder/internal/version.tag=${VERSION}"
fi

cd "${PROJECT_ROOT}"
mkdir -p "${OUT}"
OUT="$(cd "${OUT}" && pwd)"

sha256() { python3 -c 'import hashlib,sys; print(hashlib.sha256(open(sys.argv[1],"rb").read()).hexdigest())' "$1"; }

# zip rather than the `zip` command: it is absent on plenty of development
# machines, and the packages must be byte-comparable wherever they are built.
zip_dir() {
  python3 - "$1" "$2" <<'PY'
import os, sys, zipfile

src, out = sys.argv[1], sys.argv[2]
base = os.path.dirname(src.rstrip("/"))
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
            full = os.path.join(root, name)
            info = zipfile.ZipInfo.from_file(full, os.path.relpath(full, base))
            info.compress_type = zipfile.ZIP_DEFLATED
            if os.access(full, os.X_OK):
                info.external_attr = (0o755 << 16)
            else:
                info.external_attr = (0o644 << 16)
            with open(full, "rb") as fh:
                zf.writestr(info, fh.read())
PY
}

IFS=',' read -r -a target_list <<<"${TARGETS}"
for target in "${target_list[@]}"; do
  goos="${target%/*}"
  goarch="${target#*/}"
  [[ -n "${goos}" && -n "${goarch}" && "${goos}" != "${target}" ]] || die "malformed target: ${target}"

  name="jobfinder_${VERSION}_${goos}_${goarch}"
  stage="${OUT}/${name}"
  log "building ${name}"
  rm -rf "${stage}"
  mkdir -p "${stage}/configs"

  binary="${stage}/jobfinder"
  [[ "${goos}" == windows ]] && binary="${binary}.exe"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${binary}" ./cmd/jobfinder

  # Windows runs a second, GUI-subsystem build of the same source: a console
  # executable started by Task Scheduler in the user's own session is given a
  # console window, while the CLI keeps its console because that is where its
  # output goes.
  if [[ "${goos}" == windows ]]; then
    CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -ldflags "${LDFLAGS} -H=windowsgui" -o "${stage}/jobfinderw.exe" ./cmd/jobfinder
  fi

  cp LICENSE README.md "${stage}/"
  cp configs/config.example.yaml configs/profile.example.yaml "${stage}/configs/"

  # Scheduling templates and the bootstrap script ship only with the platforms
  # that have a deployment form; darwin is a build target, not one of them.
  if [[ "${goos}" == linux ]]; then
    cp -r deploy/production/systemd "${stage}/systemd"
    install -m 0755 scripts/bootstrap/install.sh "${stage}/install.sh"
  fi
  if [[ "${goos}" == windows ]]; then
    cp -r deploy/production/windows "${stage}/windows"
    cp scripts/bootstrap/install.ps1 "${stage}/install.ps1"
  fi

  if [[ "${goos}" == windows ]]; then
    rm -f "${OUT}/${name}.zip"
    zip_dir "${stage}" "${OUT}/${name}.zip"
  else
    tar -C "${OUT}" -czf "${OUT}/${name}.tar.gz" "${name}"
  fi
  rm -rf "${stage}"
done

log "packing extension"
ext_stage="${OUT}/extension"
rm -rf "${ext_stage}"
cp -r extension "${ext_stage}"
python3 - "${ext_stage}/manifest.json" "${MANIFEST_VERSION}" "${VERSION}" <<'PY'
import json, sys

path, manifest_version, version = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path, encoding="utf-8") as fh:
    manifest = json.load(fh)

manifest["version"] = manifest_version

# The fixed key is what gives a release the same extension ID on every machine.
# A deployment package must not carry it: two unpacked extensions sharing an ID
# cannot coexist in one Chrome, and the test one has to sit next to the real one.
# Without the key Chrome derives the ID from the load directory instead.
if version == "dev":
    manifest.pop("key", None)
    manifest["name"] = "jobfinder (dev)"
    manifest["action"]["default_title"] = "開啟 jobfinder（dev）"

with open(path, "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, ensure_ascii=False, indent=2)
    fh.write("\n")
PY

ext_zip="${OUT}/jobfinder-extension_${VERSION}.zip"
rm -f "${ext_zip}"
python3 - "${ext_stage}" "${ext_zip}" <<'PY'
import os, sys, zipfile

src, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as zf:
    for root, dirs, files in os.walk(src):
        dirs.sort()
        for name in sorted(files):
            full = os.path.join(root, name)
            zf.write(full, os.path.relpath(full, src))
PY
rm -rf "${ext_stage}"

log "writing SHA256SUMS"
: >"${OUT}/SHA256SUMS"
for artifact in "${OUT}"/*.tar.gz "${OUT}"/*.zip; do
  [[ -e "${artifact}" ]] || continue
  printf '%s  %s\n' "$(sha256 "${artifact}")" "$(basename "${artifact}")" >>"${OUT}/SHA256SUMS"
done

# A deployment package is installed from wherever it was copied to, so the
# bootstrap scripts travel with it; a release is installed by a script the user
# already downloaded from the release page.
if [[ "${VERSION}" == dev ]]; then
  install -m 0755 scripts/bootstrap/install.sh "${OUT}/install.sh"
  cp scripts/bootstrap/install.ps1 "${OUT}/install.ps1"
fi

log "packed ${VERSION} into ${OUT}"
ls -1 "${OUT}"
