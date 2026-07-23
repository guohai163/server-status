#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
VERSION=${1:-$(git -C "$ROOT_DIR" describe --tags --always 2>/dev/null || printf 'dev')}
VERSION=${VERSION#v}
OUTPUT_DIR=${2:-"$ROOT_DIR/dist/release"}
SOURCE="$ROOT_DIR/macos-agent/server-status-agent"
OUTPUT="$OUTPUT_DIR/server-status-agent-macos"

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
  echo "release version must be semantic versioning without a leading v: $VERSION" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"
sed "s/^AGENT_VERSION='dev'$/AGENT_VERSION='$VERSION'/" "$SOURCE" > "$OUTPUT.new"
chmod 0755 "$OUTPUT.new"
mv -f "$OUTPUT.new" "$OUTPUT"

if command -v zsh >/dev/null 2>&1; then
  zsh -n "$OUTPUT"
  [ "$(zsh "$OUTPUT" --version)" = "$VERSION" ]
fi

"$ROOT_DIR/scripts/write-release-checksums.sh" "$OUTPUT_DIR"
echo "macOS Agent release asset written to $OUTPUT"
