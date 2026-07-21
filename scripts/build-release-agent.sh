#!/bin/sh
set -eu

VERSION="${1:-$(git describe --tags --always 2>/dev/null || printf 'dev')}"
VERSION="${VERSION#v}"
OUTPUT_DIR="${2:-dist/release}"
ARCHES="${SERVER_STATUS_RELEASE_ARCHES:-amd64 arm64}"

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
  echo "release version must be semantic versioning without a leading v: $VERSION" >&2
  exit 1
fi

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

for arch in $ARCHES; do
  case "$arch" in
    amd64|arm64) ;;
    *)
      echo "unsupported release architecture: $arch" >&2
      exit 1
      ;;
  esac

  output="$OUTPUT_DIR/server-status-agent-linux-$arch"
  echo "building Agent $VERSION for linux/$arch"
  if [ "$arch" = "amd64" ]; then
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
      go build -trimpath \
        -ldflags "-s -w -X github.com/guohai/server-status/internal/agent.Version=$VERSION" \
        -o "$output" ./cmd/agent
  else
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM64=v8.0 \
      go build -trimpath \
        -ldflags "-s -w -X github.com/guohai/server-status/internal/agent.Version=$VERSION" \
        -o "$output" ./cmd/agent
  fi
  chmod 0755 "$output"
done

./scripts/write-release-checksums.sh "$OUTPUT_DIR"

echo "release assets written to $OUTPUT_DIR"
