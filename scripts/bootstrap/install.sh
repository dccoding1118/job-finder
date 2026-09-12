#!/usr/bin/env bash

# jobfinder bootstrap for Linux and macOS.
#
# This script only fetches. For the backend it downloads a release artifact,
# verifies it against the release's SHA256SUMS, unpacks it and hands over to
# `jobfinder install` (or `jobfinder update` when an installation is already
# there). Every installation decision — paths, token generation, config
# rendering, scheduling, verification — lives in those subcommands, so this
# script and its PowerShell counterpart stay thin and cannot drift apart from
# each other.
#
# The extension has no installation semantics at all: nothing is rendered, no
# token is issued, no schedule is mounted. Its whole path is therefore this
# script — download the extension artifact, verify it, unpack it into a
# resident directory, and print the Chrome steps that have no command-line
# entry point.
#
# A local package directory (`--from-dir`) is the same path with a different
# source: nothing is downloaded, the checksums come from the package's own
# SHA256SUMS, and everything after verification is identical. That is what lets
# a test environment exercise the installer a production machine will run.
#
# Doing it by hand — fetching, checking the sum, running `./jobfinder install`,
# unzipping the extension — is equivalent; this only saves those steps.
#
# Usage: install.sh [--extension | --all] [--version v1.2.3]
#                   [--repo owner/name] [--from-dir DIR] [--keep]
#   --extension  install only the extension, not the backend
#   --all        install the backend and the extension
#   --version    install a specific release instead of the latest
#   --from-dir   install from a local package directory instead of a release;
#                its SHA256SUMS is still verified, and the version comes from
#                the artifact names inside it
#   --keep       leave the unpacked files in place and print where they are

set -euo pipefail

REPO="dccoding1118/job-finder"
VERSION=""
MODE="backend"
KEEP=false
FROM_DIR=""

# The extension ID is a constant: manifest.json carries a fixed key, so Chrome
# derives the same ID on every machine and for every version.
EXTENSION_ID="oddnhajjhmgogefocnljofeahniodiei"

die() { printf '\033[31mbootstrap failed:\033[0m %s\n' "$*" >&2; exit 1; }
log() { printf '\033[1m==>\033[0m %s\n' "$*"; }

usage() { sed -n '3,35p' "$0" | sed 's/^# \{0,1\}//'; }

# The two mode flags select one mode between them, so asking for both at once
# is a contradiction rather than a last-one-wins.
set_mode() {
  [[ "${MODE}" == backend || "${MODE}" == "$1" ]] || die "--extension and --all cannot be combined"
  MODE="$1"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --extension) set_mode extension; shift ;;
    --all)       set_mode both; shift ;;
    --version)   VERSION="${2:-}"; shift 2 ;;
    --repo)      REPO="${2:-}"; shift 2 ;;
    --from-dir)  FROM_DIR="${2:-}"; shift 2 ;;
    --keep)      KEEP=true; shift ;;
    -h|--help)   usage; exit 0 ;;
    *) printf 'unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

need() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }

# A local package carries its own version and checksums, so the two flags that
# select a release have nothing left to select.
if [[ -n "${FROM_DIR}" ]]; then
  [[ -z "${VERSION}" ]] || die "--from-dir and --version cannot be combined"
  [[ -d "${FROM_DIR}" ]] || die "no such directory: ${FROM_DIR}"
  FROM_DIR="$(cd "${FROM_DIR}" && pwd)"
else
  need curl
fi

# sha256sum on Linux, shasum -a 256 on macOS: verifying the download is not
# optional, so the absence of both is a hard stop rather than a skipped check.
if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  die "neither sha256sum nor shasum is available; cannot verify the download"
fi

if [[ "${MODE}" != extension ]]; then
  need tar
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
fi
[[ "${MODE}" == backend ]] || need unzip

WORK="$(mktemp -d)"
cleanup() { [[ "${KEEP}" == true ]] || rm -rf "${WORK}"; }
trap cleanup EXIT

if [[ -n "${FROM_DIR}" ]]; then
  [[ -f "${FROM_DIR}/SHA256SUMS" ]] || die "no SHA256SUMS in ${FROM_DIR}"
  cp "${FROM_DIR}/SHA256SUMS" "${WORK}/SHA256SUMS"
  # The package names its own version: every artifact carries it, and the
  # extension zip is the one present in every package regardless of platform.
  VERSION="$(awk '{
      name=$2; sub(/^\.\//,"",name)
      if (name ~ /^jobfinder-extension_.*\.zip$/) {
        sub(/^jobfinder-extension_/,"",name); sub(/\.zip$/,"",name); print name; exit
      }
    }' "${WORK}/SHA256SUMS")"
  [[ -n "${VERSION}" ]] || die "could not derive the version from ${FROM_DIR}/SHA256SUMS"
  log "installing ${VERSION} from ${FROM_DIR}"
else
  if [[ -z "${VERSION}" ]]; then
    log "resolving the latest release of ${REPO}"
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)"
    [[ -n "${VERSION}" ]] || die "could not resolve the latest release tag"
  fi
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
  curl -fsSL -o "${WORK}/SHA256SUMS" "${BASE}/SHA256SUMS" || die "download failed: ${BASE}/SHA256SUMS"
fi

# fetch verifies before it returns, so no caller can reach an unverified file.
fetch() {
  local artifact="$1" expected actual
  if [[ -n "${FROM_DIR}" ]]; then
    log "reading ${artifact}"
    [[ -f "${FROM_DIR}/${artifact}" ]] || die "no such artifact: ${FROM_DIR}/${artifact}"
    cp "${FROM_DIR}/${artifact}" "${WORK}/${artifact}"
  else
    log "downloading ${artifact}"
    curl -fsSL -o "${WORK}/${artifact}" "${BASE}/${artifact}" || die "download failed: ${BASE}/${artifact}"
  fi
  expected="$(awk -v name="${artifact}" '$2 == name || $2 == "./" name {print $1}' "${WORK}/SHA256SUMS" | head -n 1)"
  [[ -n "${expected}" ]] || die "${artifact} is not listed in SHA256SUMS"
  actual="$(checksum "${WORK}/${artifact}")"
  [[ "${expected}" == "${actual}" ]] || die "checksum mismatch for ${artifact}: expected ${expected}, got ${actual}"
  log "checksum verified: ${artifact}"
}

install_backend() {
  local artifact="jobfinder_${VERSION}_${GOOS}_${GOARCH}.tar.gz" unpacked subcommand
  fetch "${artifact}"
  tar -C "${WORK}" -xzf "${WORK}/${artifact}"
  unpacked="${WORK}/jobfinder_${VERSION}_${GOOS}_${GOARCH}"
  [[ -x "${unpacked}/jobfinder" ]] || die "the artifact does not contain an executable at ${unpacked}/jobfinder"

  # The resident binary is the one existing-install marker both subcommands
  # agree on: `update` refuses without it, `install` is what creates it.
  if [[ -x "${HOME}/.local/bin/jobfinder" ]]; then
    subcommand=update
  else
    subcommand=install
  fi
  log "handing over to jobfinder ${subcommand}"
  "${unpacked}/jobfinder" "${subcommand}"

  if [[ "${KEEP}" == true ]]; then
    printf '\nUnpacked artifact kept at %s\n' "${unpacked}"
  fi
}

install_extension() {
  local artifact="jobfinder-extension_${VERSION}.zip" dest
  fetch "${artifact}"
  # The resident directory follows the same data home the backend resolves, and
  # is per-tag so an older unpack stays intact until Chrome points at the new one.
  dest="${XDG_DATA_HOME:-${HOME}/.local/share}/jobfinder/extension/${VERSION}"
  mkdir -p "${dest}"
  unzip -qo "${WORK}/${artifact}" -d "${dest}"
  [[ -f "${dest}/manifest.json" ]] || die "the artifact does not contain a manifest at ${dest}/manifest.json"
  log "extension unpacked at ${dest}"

  # Chrome has no command-line entry point for loading an unpacked extension,
  # so the script stops at a prepared directory and the steps to point Chrome
  # at it. Removing the old card and refilling Options stay manual.
  cat <<EOF

The extension directory is ready. Finish in Chrome:
  1. open chrome://extensions and turn on Developer mode
  2. remove the previous jobfinder card, if there is one
  3. Load unpacked -> ${dest}
  4. Details -> Extension options: fill in the API endpoint and token
EOF

  # A release manifest carries a fixed key, so its ID is the same everywhere and
  # can be printed here. A package built for testing has no key — Chrome derives
  # the ID from the load directory, so only Chrome can tell you what it is.
  if grep -q '"key"' "${dest}/manifest.json"; then
    cat <<EOF

The extension ID is fixed by the manifest key:
  ${EXTENSION_ID}
The backend only answers requests from it once its config carries
  api.extension_origin: chrome-extension://${EXTENSION_ID}
EOF
  else
    cat <<EOF

This package has no manifest key, so Chrome derives the extension ID from the
directory above. Read the ID off chrome://extensions after loading it, then put
it in the backend config as
  api.extension_origin: chrome-extension://<the id Chrome shows>
Moving the directory changes the ID, so leave it where it is.
EOF
  fi

  if [[ "${KEEP}" == true ]]; then
    printf '\nArchive kept at %s\n' "${WORK}/${artifact}"
  fi
}

[[ "${MODE}" == extension ]] || install_backend
[[ "${MODE}" == backend ]] || install_extension
