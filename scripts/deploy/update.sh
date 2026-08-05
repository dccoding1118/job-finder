#!/usr/bin/env bash

# Update an existing install to the current development checkout. The build gate
# and the hand-over are identical to install.sh; only the subcommand differs.
#
# Usage: scripts/deploy/update.sh [--skip-tests]

set -euo pipefail
JOBFINDER_DEPLOY_ACTION=update exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install.sh" "$@"
