#!/bin/bash
set -e

# ==============================================================================
# Script: update_xio.sh
# Purpose: Update vendored X-IO packages in Go module cache with local code
# ==============================================================================

# ------------------------------------------------------------------------------
# Constants and Formatting
# ------------------------------------------------------------------------------

YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
WORKDIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
XIO_DIR="$WORKDIR/NFs/xio"

if ! command -v go >/dev/null 2>&1; then
  echo "[ERROR] Go is not on PATH. Source the Go environment installed by ONVM first."
  exit 1
fi

GOMOD_DIR="$(go env GOMODCACHE)/github.com/nycu-ucr"

# ------------------------------------------------------------------------------
# Check if target directory exists
# ------------------------------------------------------------------------------

if [ ! -d "$GOMOD_DIR" ]; then
  echo -e "${YELLOW}[WARN] Go module directory not found at $GOMOD_DIR.${NC}"
  echo -e "       Ensure Go modules have been downloaded before running this script."
  exit 1
fi

if [ ! -f "$XIO_DIR/poller.go" ] || [ ! -f "$XIO_DIR/onvm_poller.c" ]; then
  echo "[ERROR] Local X-IO source is missing from $XIO_DIR."
  echo "        Restore and initialize it with:"
  echo "        git submodule update --init --checkout NFs/xio"
  exit 1
fi

echo -e "[INFO] Scanning vendored modules under ${YELLOW}$GOMOD_DIR${NC}..."

# ------------------------------------------------------------------------------
# X-IO Replacement Loop
# ------------------------------------------------------------------------------
cd "$GOMOD_DIR"
for target in onvmpoller@*; do
    [ -d "$target" ] || continue
    echo -e "[INFO] Processing module: ${YELLOW}${target}${NC}"
    cd "$GOMOD_DIR"

    # The Go module cache is normally read-only. Make this copied module
    # writable, then overlay the locally built ONVM-compatible X-IO source.
    chmod -R u+w "$target"
    cp -R "$XIO_DIR"/* "$target"

    # Perform path replacements
    cd "$target"
    echo -e "[INFO] Replacing hardcoded paths with ${YELLOW}$HOME${NC}"
    sed -i "s#/home/hstsai#$HOME#g" poller.go
    sed -i "s#/home/hstsai#$HOME#g" onvm_poller.c
    sed -i "s#/home/johnson#$HOME#g" poller.go
    sed -i "s#/home/johnson#$HOME#g" onvm_poller.c

    # Remove potentially problematic Go file
    rm -f listen.go
done
