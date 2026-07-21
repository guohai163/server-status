#!/bin/sh
set -eu

OUTPUT_DIR="${1:-dist/release}"
checksums="$OUTPUT_DIR/checksums.txt"
temporary="$OUTPUT_DIR/.checksums.txt.new"
: > "$temporary"

for file in "$OUTPUT_DIR"/server-status-agent-*; do
  [ -f "$file" ] || continue
  name=$(basename "$file")
  if command -v sha256sum >/dev/null 2>&1; then
    digest=$(sha256sum "$file" | awk '{print $1}')
  else
    digest=$(shasum -a 256 "$file" | awk '{print $1}')
  fi
  printf '%s  %s\n' "$digest" "$name" >> "$temporary"
done

mv -f "$temporary" "$checksums"
