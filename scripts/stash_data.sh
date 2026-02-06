#!/bin/bash
# stash_data.sh - Stash user-generated data into a hidden directory
#
# Idempotent: safe to run multiple times. Already-stashed items are skipped.
# Counterpart: scripts/restore_data.sh
#
# This moves the following into .data_stash/:
#   - benchmarks/spider/database/        (downloaded Spider DBs)
#   - benchmarks/bird/dev/dev_databases/  (downloaded BIRD DBs)
#   - contexts/sqlite/                    (generated Rich Context files)
#   - results/                            (experiment results)
#
# The following are NOT stashed (repo baselines, cannot be regenerated):
#   - benchmarks/spider_corrected/        (corrected dev set)
#   - contexts/DATA_QUALITY_REPORT.md     (quality report)
#   - dbs/spider/                         (database configs)
#
# After stashing, the repo looks like a fresh clone — ready for a clean run.
#
# Usage: bash scripts/stash_data.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
STASH_DIR="$PROJECT_ROOT/.data_stash"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info() { echo -e "${GREEN}[STASH]${NC} $1"; }
skip() { echo -e "${YELLOW}[SKIP]${NC}  $1 (already stashed or not present)"; }

mkdir -p "$STASH_DIR"

stash_item() {
    local src="$PROJECT_ROOT/$1"
    local dst="$STASH_DIR/$1"

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
    info "$1 -> .data_stash/$1"
}

echo "Stashing user-generated data into .data_stash/ ..."
echo ""

# Downloaded databases (large, re-downloadable)
stash_item "benchmarks/spider/database"
stash_item "benchmarks/bird/dev/dev_databases"

# Generated Rich Context (can be regenerated via gen_rich_context_*)
stash_item "contexts/sqlite"

# Experiment results (can be regenerated via eval_*)
stash_item "results"

echo ""
info "Done. Repo is now in 'fresh clone' state."
info "Kept in place: benchmarks/spider_corrected/, contexts/DATA_QUALITY_REPORT.md, dbs/spider/"
info "Run 'bash scripts/restore_data.sh' to put everything back."
