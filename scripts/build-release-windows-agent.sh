#!/bin/sh
set -eu

VERSION="${1:-$(git describe --tags --always 2>/dev/null || printf 'dev')}"
VERSION="${VERSION#v}"
OUTPUT_DIR="${2:-dist/release}"
ARCHES="${SERVER_STATUS_WINDOWS_RELEASE_ARCHES:-amd64}"
ROOT_DIR=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

case "$OUTPUT_DIR" in
  /*) ;;
  *) OUTPUT_DIR="$ROOT_DIR/$OUTPUT_DIR" ;;
esac

if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
  echo "release version must be semantic versioning without a leading v: $VERSION" >&2
  exit 1
fi

case "$(go version)" in
  "go version go1.20.14 "*) ;;
  *)
    echo "Windows Server 2008 R2 compatibility builds require exactly Go 1.20.14" >&2
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
  echo "building Windows Agent $VERSION for windows/$arch"
  (
    cd "$ROOT_DIR/windows-agent"
    CGO_ENABLED=0 GOOS=windows GOARCH="$arch" GO386=softfloat \
      go build -trimpath \
        -ldflags "-s -w -X main.Version=windows-legacy-$VERSION" \
        -o "$output" .
  )
done

"$ROOT_DIR/scripts/fetch-smartmontools-windows.sh" "$OUTPUT_DIR"
"$ROOT_DIR/scripts/write-release-checksums.sh" "$OUTPUT_DIR"
echo "Windows Agent release assets written to $OUTPUT_DIR"
