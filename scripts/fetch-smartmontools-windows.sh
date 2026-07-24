#!/bin/sh
set -eu

OUTPUT_DIR=${1:-dist/release}
SMARTMONTOOLS_VERSION=7.5
SMARTMONTOOLS_RELEASE=RELEASE_7_5
SMARTMONTOOLS_BASE_URL="https://github.com/smartmontools/smartmontools/releases/download/$SMARTMONTOOLS_RELEASE"
SMARTMONTOOLS_SETUP_SHA256=896337fcc253220614cf8cdbd5cf2321c5aa326a37a04160a672a281e6104c70
SMARTMONTOOLS_SOURCE_SHA256=690b83ca331378da9ea0d9d61008c4b22dde391387b9bbad7f29387f2595f76e

mkdir -p "$OUTPUT_DIR"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

fetch_verified() {
  fetch_url=$1
  fetch_expected=$2
  fetch_destination=$3

  if [ -f "$fetch_destination" ] && [ "$(sha256_file "$fetch_destination")" = "$fetch_expected" ]; then
    chmod 0644 "$fetch_destination"
    return
  fi

  fetch_temporary=$(mktemp "$OUTPUT_DIR/.smartmontools-download.XXXXXX")
  if command -v curl >/dev/null 2>&1; then
    if ! curl -fL --retry 3 --connect-timeout 20 "$fetch_url" -o "$fetch_temporary"; then
      rm -f "$fetch_temporary"
      exit 1
    fi
  elif command -v wget >/dev/null 2>&1; then
    if ! wget -O "$fetch_temporary" "$fetch_url"; then
      rm -f "$fetch_temporary"
      exit 1
    fi
  else
    rm -f "$fetch_temporary"
    echo "curl or wget is required to download smartmontools" >&2
    exit 1
  fi

  fetch_actual=$(sha256_file "$fetch_temporary")
  if [ "$fetch_actual" != "$fetch_expected" ]; then
    rm -f "$fetch_temporary"
    echo "smartmontools SHA-256 mismatch: expected $fetch_expected, got $fetch_actual" >&2
    exit 1
  fi
  mv -f "$fetch_temporary" "$fetch_destination"
  chmod 0644 "$fetch_destination"
}

fetch_verified \
  "$SMARTMONTOOLS_BASE_URL/smartmontools-$SMARTMONTOOLS_VERSION.win32-setup.exe" \
  "$SMARTMONTOOLS_SETUP_SHA256" \
  "$OUTPUT_DIR/server-status-smartctl-windows-setup.exe"
fetch_verified \
  "$SMARTMONTOOLS_BASE_URL/smartmontools-$SMARTMONTOOLS_VERSION.tar.gz" \
  "$SMARTMONTOOLS_SOURCE_SHA256" \
  "$OUTPUT_DIR/server-status-smartctl-source.tar.gz"

echo "smartmontools $SMARTMONTOOLS_VERSION Windows assets written to $OUTPUT_DIR"
