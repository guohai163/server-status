#!/bin/sh
set -eu

VERSION="${1:-$(git describe --tags --always 2>/dev/null || printf 'dev')}"
VERSION="${VERSION#v}"
OUTPUT_DIR="${2:-dist/release}"
ARCHES="${SERVER_STATUS_WINDOWS_RELEASE_ARCHES:-386 amd64}"

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
  echo "release version must be semantic versioning without a leading v: $VERSION" >&2
  exit 1
fi

case "$(go version)" in
  "go version go1.10.8 "*) ;;
  *)
    echo "Windows Server 2003/2008 builds require exactly Go 1.10.8" >&2
    exit 1
    ;;
esac

mkdir -p "$OUTPUT_DIR"
for arch in $ARCHES; do
  case "$arch" in
    386|amd64) ;;
    *)
      echo "unsupported Windows architecture: $arch" >&2
      exit 1
      ;;
  esac
  output="$OUTPUT_DIR/server-status-agent-windows-$arch.exe"
  echo "building legacy Windows Agent $VERSION for windows/$arch"
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" GO386=387 \
    go build \
      -ldflags "-s -w -X main.Version=windows-legacy-$VERSION" \
      -o "$output" ./windows-agent
done

./scripts/write-release-checksums.sh "$OUTPUT_DIR"
echo "Windows Agent release assets written to $OUTPUT_DIR"
