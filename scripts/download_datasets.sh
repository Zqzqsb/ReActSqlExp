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
# Usage: bash scripts/download_datasets.sh [--proxy host:port]
#
# Options:
#   --proxy HOST:PORT    Use proxy for downloads (default: 127.0.0.1:7890)

set -e

PROXY=""
USE_PROXY=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --proxy)
            USE_PROXY=true
            if [[ -n "$2" && "$2" != --* ]]; then
                PROXY="$2"
                shift
            else
                PROXY="127.0.0.1:7890"
            fi
            shift
            ;;
        *)
            echo "Unknown option: $1"
            echo "Usage: bash scripts/download_datasets.sh [--proxy host:port]"
            exit 1
            ;;
    esac
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

if [ "$USE_PROXY" = true ]; then
    info "Using proxy: $PROXY"
fi

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
        if [ "$USE_PROXY" = true ]; then
            https_proxy="http://$PROXY" http_proxy="http://$PROXY" gdown "$file_id" -O "$output"
        else
            gdown "$file_id" -O "$output"
        fi
    elif [ "$DOWNLOADER" = "curl" ]; then
        local proxy_args=""
        if [ "$USE_PROXY" = true ]; then
            proxy_args="-x http://$PROXY"
        fi
        
        # For large files, Google Drive requires a cookie-based confirmation.
        # Step 1: request the file, capture cookies
        local cookie_file
        cookie_file=$(mktemp)
        curl -sc "$cookie_file" -L $proxy_args \
            "https://drive.google.com/uc?export=download&id=${file_id}" -o /dev/null 2>/dev/null

        # Step 2: extract the confirmation token from cookies or response
        local confirm_code
        confirm_code=$(grep -oP 'download_warning_[^\s]*\s+\K[^\s]+' "$cookie_file" 2>/dev/null || true)
        if [ -z "$confirm_code" ]; then
            confirm_code="t"
        fi

        # Step 3: download with the confirmation token and cookies
        curl -Lb "$cookie_file" --progress-bar $proxy_args -o "$output" \
            "https://drive.usercontent.google.com/download?id=${file_id}&export=download&confirm=${confirm_code}"
        rm -f "$cookie_file"
    else
        local proxy_args=""
        if [ "$USE_PROXY" = true ]; then
            proxy_args="-e use_proxy=yes -e http_proxy=http://$PROXY -e https_proxy=http://$PROXY"
        fi
        
        wget --progress=bar:force $proxy_args -O "$output" \
            "https://drive.usercontent.google.com/download?id=${file_id}&export=download&confirm=t" 2>&1
    fi
}

# verify_zip FILE_PATH DESCRIPTION
verify_zip() {
    local filepath="$1"
    local desc="$2"
    # Primary check: use file magic
    if ! file "$filepath" | grep -qi "zip"; then
        # Secondary check: look at file header (PK magic bytes)
        if ! head -c 4 "$filepath" | grep -qP '\x50\x4b\x03\x04' 2>/dev/null; then
            # Check if it's an HTML page (Google Drive error/confirmation page)
            if head -c 200 "$filepath" | grep -qi '<html'; then
                rm -f "$filepath"
                error "Downloaded $desc is an HTML page, not a zip file.\n  Google Drive returned an error or confirmation page.\n  Install gdown (pip install gdown) and retry, or download manually."
            fi
            rm -f "$filepath"
            error "Downloaded $desc is not a valid zip file.\n  Google Drive may have returned an HTML error page.\n  Install gdown (pip install gdown) and retry, or download manually."
        fi
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
    SPIDER_EXTRACT_DIR="$PROJECT_ROOT/benchmarks/spider/_extract_tmp"
    mkdir -p "$SPIDER_EXTRACT_DIR"
    unzip -q "$SPIDER_TMP" -d "$SPIDER_EXTRACT_DIR" || error "unzip failed for Spider. The file may be corrupted."
    rm -f "$SPIDER_TMP"
    # Clean up macOS artifacts
    rm -rf "$SPIDER_EXTRACT_DIR/__MACOSX"

    # Handle different zip structures: look for "database" directory anywhere in extracted content
    # Known structures: spider/database/*, spider_data/database/*
    if [ ! -d "$SPIDER_DB_DIR" ]; then
        DB_FOUND=$(find "$SPIDER_EXTRACT_DIR" -type d -name "database" | head -1)
        if [ -n "$DB_FOUND" ]; then
            mv "$DB_FOUND" "$SPIDER_DB_DIR"
        fi
    fi
    # Clean up temp extraction directory
    rm -rf "$SPIDER_EXTRACT_DIR"

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
    BIRD_EXTRACT_DIR="$(dirname "$BIRD_DB_DIR")/_bird_extract_tmp"
    mkdir -p "$BIRD_EXTRACT_DIR"
    unzip -q "$BIRD_TMP" -d "$BIRD_EXTRACT_DIR" || error "unzip failed for BIRD databases. The file may be corrupted."
    rm -f "$BIRD_TMP"
    # Clean up macOS artifacts
    rm -rf "$BIRD_EXTRACT_DIR/__MACOSX"

    # Handle different zip structures: look for dev_databases anywhere in extracted content
    if [ ! -d "$BIRD_DB_DIR" ]; then
        BIRD_FOUND=$(find "$BIRD_EXTRACT_DIR" -type d -name "dev_databases" | head -1)
        if [ -n "$BIRD_FOUND" ]; then
            mv "$BIRD_FOUND" "$BIRD_DB_DIR"
        else
            # If there's no dev_databases folder, check if the zip directly contains DB folders
            # (e.g., folders with .sqlite files inside)
            SQLITE_FOUND=$(find "$BIRD_EXTRACT_DIR" -name "*.sqlite" -type f | head -1)
            if [ -n "$SQLITE_FOUND" ]; then
                # Find the parent directory that contains all DB subdirectories
                SQLITE_PARENT=$(dirname "$(dirname "$SQLITE_FOUND")")
                if [ "$SQLITE_PARENT" = "$BIRD_EXTRACT_DIR" ]; then
                    # DB folders are directly in extract dir
                    mv "$BIRD_EXTRACT_DIR" "$BIRD_DB_DIR"
                else
                    mv "$SQLITE_PARENT" "$BIRD_DB_DIR"
                fi
            fi
        fi
    fi
    # Clean up temp extraction directory if it still exists
    rm -rf "$BIRD_EXTRACT_DIR"

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
echo ""
info "Next steps:"
echo "  1. cp llm_config.json.example llm_config.json  # Configure your LLM API key"
echo "  2. go run ./cmd/gen_all_dev                    # Generate Rich Context (interactive)"
echo "  3. go run ./cmd/eval                           # Run evaluation (interactive)"
