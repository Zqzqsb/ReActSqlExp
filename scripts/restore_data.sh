#!/bin/bash
# restore_data.sh - Restore stashed data back to working directories
#
# Idempotent: safe to run multiple times. Already-restored items are skipped.
# Counterpart: scripts/stash_data.sh
#
# Usage: bash scripts/restore_data.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
STASH_DIR="$PROJECT_ROOT/.data_stash"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info() { echo -e "${GREEN}[RESTORE]${NC} $1"; }
skip() { echo -e "${YELLOW}[SKIP]${NC}    $1 (already in place or not in stash)"; }

if [ ! -d "$STASH_DIR" ]; then
    echo -e "${RED}[ERROR]${NC} No stash found at .data_stash/. Nothing to restore."
    exit 1
fi

restore_item() {
    local src="$STASH_DIR/$1"
    local dst="$PROJECT_ROOT/$1"

    if [ ! -e "$src" ]; then
        skip "$1"
        return
    fi

    if [ -e "$dst" ]; then
        skip "$1"
        return
    fi

    mkdir -p "$(dirname "$dst")"
    mv "$src" "$dst"
    info ".data_stash/$1 -> $1"
}

echo "Restoring data from .data_stash/ ..."
echo ""

restore_item "benchmarks/spider/database"
restore_item "benchmarks/bird/dev/dev_databases"
restore_item "contexts/sqlite"
restore_item "results"

echo ""

# Clean up empty directories in stash
find "$STASH_DIR" -type d -empty -delete 2>/dev/null || true

if [ -d "$STASH_DIR" ]; then
    info "Some items remain in .data_stash/."
else
    info "Stash directory cleaned up."
fi

echo ""
info "Done. All data restored."
