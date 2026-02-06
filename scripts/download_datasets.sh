#!/bin/bash
# download_datasets.sh - Download benchmark databases for ReAct SQL experiments
#
# Downloads:
#   1. Spider 1.0 database files -> benchmarks/spider/database/
#   2. BIRD dev database files   -> benchmarks/bird/dev/dev_databases/
#
# Note: The corrected Spider dev set (questions + gold SQL + field descriptions)
#       is already included in the repository.
#
# Prerequisites: curl (or wget), unzip
# Usage: bash scripts/download_datasets.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

command -v unzip >/dev/null 2>&1 || error "unzip is required. Install: sudo apt install unzip"

# Prefer curl, fall back to wget
if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
else
    error "curl or wget is required. Install: sudo apt install curl"
fi

# download_file URL OUTPUT_PATH DESCRIPTION
download_file() {
    local url="$1"
    local output="$2"
    local desc="$3"

    info "Downloading $desc ..."
    if [ "$DOWNLOADER" = "curl" ]; then
        curl -L --progress-bar -o "$output" "$url"
    else
        wget --progress=bar:force -O "$output" "$url" 2>&1
    fi
}

# ============================================================
# 1. Spider 1.0 databases (~840 MB)
#    Official source: https://yale-lily.github.io/spider
#    Google Drive file ID: 1403EGqzIDoHMdQF4c9Bkyl7dZLZ5Wt6J
# ============================================================

SPIDER_DB_DIR="$PROJECT_ROOT/benchmarks/spider/database"

if [ -d "$SPIDER_DB_DIR" ] && [ "$(ls -A "$SPIDER_DB_DIR" 2>/dev/null)" ]; then
    warn "Spider databases already exist at $SPIDER_DB_DIR, skipping."
else
    SPIDER_TMP="$PROJECT_ROOT/benchmarks/spider/_spider_tmp.zip"

    # Google Drive: use confirm=t to bypass large file warning
    download_file \
        "https://drive.usercontent.google.com/download?id=1403EGqzIDoHMdQF4c9Bkyl7dZLZ5Wt6J&export=download&confirm=t" \
        "$SPIDER_TMP" \
        "Spider 1.0 dataset (~840 MB)" \
        || error "Failed to download Spider. Please download manually:\n  1. Go to https://yale-lily.github.io/spider\n  2. Download the dataset zip\n  3. Extract database/ into benchmarks/spider/"

    # Verify it's actually a zip file (not an HTML error page)
    if ! file "$SPIDER_TMP" | grep -qi "zip"; then
        rm -f "$SPIDER_TMP"
        error "Downloaded file is not a valid zip. Google Drive may have blocked the download.\n  Please download manually from https://yale-lily.github.io/spider"
    fi

    info "Extracting Spider databases..."
    unzip -q "$SPIDER_TMP" "spider/database/*" -d "$PROJECT_ROOT/benchmarks/" 2>/dev/null \
        || unzip -q "$SPIDER_TMP" -d "$PROJECT_ROOT/benchmarks/spider/_extract_tmp"

    # Handle different zip structures
    if [ ! -d "$SPIDER_DB_DIR" ]; then
        if [ -d "$PROJECT_ROOT/benchmarks/spider/_extract_tmp" ]; then
            DB_FOUND=$(find "$PROJECT_ROOT/benchmarks/spider/_extract_tmp" -type d -name "database" | head -1)
            if [ -n "$DB_FOUND" ]; then
                mv "$DB_FOUND" "$SPIDER_DB_DIR"
            fi
            rm -rf "$PROJECT_ROOT/benchmarks/spider/_extract_tmp"
        fi
    fi

    rm -f "$SPIDER_TMP"

    if [ -d "$SPIDER_DB_DIR" ] && [ "$(ls -A "$SPIDER_DB_DIR")" ]; then
        info "Spider databases ready. ($(ls "$SPIDER_DB_DIR" | wc -l) databases)"
    else
        error "Extraction failed. Please download manually from https://yale-lily.github.io/spider"
    fi
fi

# ============================================================
# 2. BIRD dev databases (~1.4 GB)
#    Official source: https://bird-bench.github.io/
# ============================================================

BIRD_DB_DIR="$PROJECT_ROOT/benchmarks/bird/dev/dev_databases"

if [ -d "$BIRD_DB_DIR" ] && [ "$(ls -A "$BIRD_DB_DIR" 2>/dev/null)" ]; then
    warn "BIRD databases already exist at $BIRD_DB_DIR, skipping."
else
    BIRD_TMP="$PROJECT_ROOT/benchmarks/bird/dev/dev_databases.zip"

    download_file \
        "https://bird-bench.oss-cn-beijing.aliyuncs.com/dev20240627/dev_databases.zip" \
        "$BIRD_TMP" \
        "BIRD dev databases (~1.4 GB)" \
        || error "Failed to download BIRD. Please download from https://bird-bench.github.io/"

    info "Extracting BIRD databases..."
    unzip -q "$BIRD_TMP" -d "$(dirname "$BIRD_DB_DIR")"
    rm -f "$BIRD_TMP"
    # Clean up macOS artifacts
    rm -rf "$PROJECT_ROOT/benchmarks/bird/dev/__MACOSX"

    info "BIRD databases ready. ($(ls "$BIRD_DB_DIR" | wc -l) databases)"
fi

# ============================================================
# Summary
# ============================================================

echo ""
info "All datasets ready!"
echo ""
echo "  Spider databases:      $SPIDER_DB_DIR"
echo "  BIRD databases:        $BIRD_DB_DIR"
echo "  Corrected Spider dev:  $PROJECT_ROOT/benchmarks/spider_corrected/ (already in repo)"
echo "  Quality report:        $PROJECT_ROOT/contexts/DATA_QUALITY_REPORT.md"
echo ""
info "Next steps:"
echo "  1. cp llm_config.json.example llm_config.json  # Configure your LLM API key"
echo "  2. go run ./cmd/gen_rich_context_spider --config dbs/spider/concert_singer.json"
echo "  3. go run ./cmd/eval_spider --use-rich-context --use-react"
