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

# Check for gdown (for reliable Google Drive downloads)
if command -v gdown >/dev/null 2>&1; then
    HAS_GDOWN=true
else
    HAS_GDOWN=false
    warn "gdown not found. Install: pip install gdown"
    warn "Falling back to curl/wget for Google Drive (may fail for large files)."
fi

# Prefer curl, fall back to wget
if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
else
    error "curl or wget is required. Install: sudo apt install curl"
fi

# download_gdrive GOOGLE_DRIVE_FILE_ID OUTPUT_PATH DESCRIPTION
download_gdrive() {
    local file_id="$1"
    local output="$2"
    local desc="$3"

    info "Downloading $desc ..."
    if [ "$HAS_GDOWN" = true ]; then
        gdown "$file_id" -O "$output"
    elif [ "$DOWNLOADER" = "curl" ]; then
        curl -L --progress-bar -o "$output" \
            "https://drive.usercontent.google.com/download?id=${file_id}&export=download&confirm=t"
    else
        wget --progress=bar:force -O "$output" \
            "https://drive.usercontent.google.com/download?id=${file_id}&export=download&confirm=t" 2>&1
    fi
}

# verify_zip FILE_PATH DESCRIPTION
verify_zip() {
    local filepath="$1"
    local desc="$2"
    if ! file "$filepath" | grep -qi "zip"; then
        rm -f "$filepath"
        error "Downloaded $desc is not a valid zip file.\n  Google Drive may have returned an HTML error page.\n  Install gdown (pip install gdown) and retry, or download manually."
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

    # Google Drive file ID for Spider 1.0
    download_gdrive "1403EGqzIDoHMdQF4c9Bkyl7dZLZ5Wt6J" \
        "$SPIDER_TMP" \
        "Spider 1.0 dataset (~840 MB)" \
        || error "Failed to download Spider. Please download manually:\n  1. Go to https://yale-lily.github.io/spider\n  2. Download the dataset zip\n  3. Extract database/ into benchmarks/spider/"

    verify_zip "$SPIDER_TMP" "Spider dataset"

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
    BIRD_TMP="$PROJECT_ROOT/benchmarks/bird/dev/_bird_tmp.zip"

    # Google Drive file ID for BIRD dev databases (from official HuggingFace page)
    download_gdrive "13VLWIwpw5E3d5DUkMvzw7hvHE67a4XkG" \
        "$BIRD_TMP" \
        "BIRD dev databases (~1.4 GB)" \
        || error "Failed to download BIRD.\n  Please download manually from:\n  https://drive.google.com/file/d/13VLWIwpw5E3d5DUkMvzw7hvHE67a4XkG"

    verify_zip "$BIRD_TMP" "BIRD databases"

    info "Extracting BIRD databases..."
    unzip -q "$BIRD_TMP" -d "$(dirname "$BIRD_DB_DIR")"
    rm -f "$BIRD_TMP"
    # Clean up macOS artifacts
    rm -rf "$PROJECT_ROOT/benchmarks/bird/dev/__MACOSX"

    # Handle different zip structures: the zip might contain a top-level folder
    if [ ! -d "$BIRD_DB_DIR" ]; then
        BIRD_FOUND=$(find "$(dirname "$BIRD_DB_DIR")" -maxdepth 2 -type d -name "dev_databases" | head -1)
        if [ -n "$BIRD_FOUND" ] && [ "$BIRD_FOUND" != "$BIRD_DB_DIR" ]; then
            mv "$BIRD_FOUND" "$BIRD_DB_DIR"
        fi
    fi

    if [ -d "$BIRD_DB_DIR" ] && [ "$(ls -A "$BIRD_DB_DIR")" ]; then
        info "BIRD databases ready. ($(ls "$BIRD_DB_DIR" | wc -l) databases)"
    else
        error "Extraction failed. Please download manually from:\n  https://drive.google.com/file/d/13VLWIwpw5E3d5DUkMvzw7hvHE67a4XkG"
    fi
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
