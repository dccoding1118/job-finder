#!/usr/bin/env bash

# jobfinder bootstrap for Linux and macOS.
#
# This script only fetches: it downloads a release artifact, verifies it against
# the release's SHA256SUMS, unpacks it, and hands over to `jobfinder install`.
# Every installation decision — paths, token generation, config rendering,
# scheduling, verification — lives in that subcommand, so this script and its
# PowerShell counterpart stay thin and cannot drift apart from each other.
#
# Downloading the artifact by hand and running `./jobfinder install` yourself is
# equivalent; this only saves the download and the checksum check.
#
# Usage: install.sh [--version v1.2.3] [--repo owner/name] [--keep]
#   --version  install a specific release instead of the latest
#   --keep     leave the unpacked artifact in place and print where it is

set -euo pipefail

REPO="dccoding1118/job-finder"
VERSION=""
KEEP=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --repo)    REPO="${2:-}"; shift 2 ;;
    --keep)    KEEP=true; shift ;;
    -h|--help) sed -n '3,17p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

die() { printf '\033[31mbootstrap failed:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1m==>\033[0m %s\n' "$*"; }

need() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
need curl
need tar

# sha256sum on Linux, shasum -a 256 on macOS: verifying the download is not
# optional, so the absence of both is a hard stop rather than a skipped check.
if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "neither sha256sum nor shasum is available; cannot verify the download"
fi

case "$(uname -s)" in
  Linux)  GOOS=linux ;;
  Darwin) GOOS=darwin ;;
  *) die "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) GOARCH=amd64 ;;
  arm64|aarch64) GOARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m)" ;;
esac

if [[ -z "${VERSION}" ]]; then
  log "resolving the latest release of ${REPO}"
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
  [[ -n "${VERSION}" ]] || die "could not resolve the latest release tag"
fi

ARTIFACT="jobfinder_${VERSION}_${GOOS}_${GOARCH}.tar.gz"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"
WORK="$(mktemp -d)"
cleanup() { [[ "${KEEP}" == true ]] || rm -rf "${WORK}"; }
trap cleanup EXIT

log "downloading ${ARTIFACT}"
curl -fsSL -o "${WORK}/${ARTIFACT}" "${BASE}/${ARTIFACT}" || die "download failed: ${BASE}/${ARTIFACT}"
curl -fsSL -o "${WORK}/SHA256SUMS" "${BASE}/SHA256SUMS" || die "download failed: ${BASE}/SHA256SUMS"

expected="$(awk -v name="${ARTIFACT}" '$2 == name || $2 == "./" name {print $1}' "${WORK}/SHA256SUMS" | head -n 1)"
[[ -n "${expected}" ]] || die "${ARTIFACT} is not listed in SHA256SUMS"
actual="$(checksum "${WORK}/${ARTIFACT}")"
[[ "${expected}" == "${actual}" ]] || die "checksum mismatch for ${ARTIFACT}: expected ${expected}, got ${actual}"
log "checksum verified"

tar -C "${WORK}" -xzf "${WORK}/${ARTIFACT}"
UNPACKED="${WORK}/jobfinder_${VERSION}_${GOOS}_${GOARCH}"
[[ -x "${UNPACKED}/jobfinder" ]] || die "the artifact does not contain an executable at ${UNPACKED}/jobfinder"

log "handing over to jobfinder install"
"${UNPACKED}/jobfinder" install

if [[ "${KEEP}" == true ]]; then
  printf '\nUnpacked artifact kept at %s\n' "${UNPACKED}"
fi
