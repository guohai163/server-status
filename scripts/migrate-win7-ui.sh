#!/bin/sh
set -eu

# Run this script on an operator workstation that already has SSH access to both hosts.
SOURCE_HOST="${WIN7_UI_SOURCE_HOST:-root@10.12.54.140}"
SOURCE_PORT="${WIN7_UI_SOURCE_PORT:-63008}"
TARGET_HOST="${WIN7_UI_TARGET_HOST:-gydev@10.12.54.221}"
TARGET_PORT="${WIN7_UI_TARGET_PORT:-63008}"
VM_NAME="${WIN7_UI_VM_NAME:-win7-ui}"
SOURCE_IMAGE="${WIN7_UI_SOURCE_IMAGE:-/backup/kvm/win10-ui.qcow}"
TARGET_BASE="${WIN7_UI_TARGET_BASE:-/data/kvm}"
TARGET_DIR="$TARGET_BASE/$VM_NAME"
TARGET_IMAGE="$TARGET_DIR/$VM_NAME.qcow"
TARGET_XML="$TARGET_DIR/$VM_NAME.xml"
BRIDGE="${WIN7_UI_BRIDGE:-br0}"
VNC_PORT="${WIN7_UI_VNC_PORT:-50010}"
TARGET_MACHINE="${WIN7_UI_TARGET_MACHINE:-pc-i440fx-jammy}"
EXECUTE=0
START_VM=0
TMP_DIR=""

usage() {
  cat <<'USAGE'
Usage: scripts/migrate-win7-ui.sh [--execute] [--start]

Without --execute, run only read-only preflight checks.
--execute copies the powered-off qcow2, verifies SHA-256 and qcow2 integrity,
then defines the target libvirt domain.
--start starts the newly defined target VM. It requires --execute.

Configuration is supplied with WIN7_UI_* environment variables. See
docs/win7-ui-migration.md for the supported variables and rollback procedure.
USAGE
}

source_ssh() {
  ssh -p "$SOURCE_PORT" -o BatchMode=yes -o ConnectTimeout=15 "$SOURCE_HOST" "$@"
}

target_ssh() {
  ssh -p "$TARGET_PORT" -o BatchMode=yes -o ConnectTimeout=15 "$TARGET_HOST" "$@"
}

fail() {
  echo "error: $*" >&2
  exit 1
}

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

while [ "$#" -gt 0 ]; do
  case "$1" in
    --execute)
      EXECUTE=1
      ;;
    --start)
      START_VM=1
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      fail "unknown argument: $1"
      ;;
  esac
  shift
done

if [ "$START_VM" -eq 1 ] && [ "$EXECUTE" -ne 1 ]; then
  fail "--start requires --execute"
fi

if ! command -v ssh >/dev/null 2>&1 || ! command -v scp >/dev/null 2>&1; then
  fail "ssh and scp are required on the operator workstation"
fi

echo "[1/6] Checking source VM and image"
echo "  - checking that $VM_NAME is shut off"
SOURCE_STATE=$(source_ssh "LC_ALL=C virsh domstate '$VM_NAME'")
[ "$SOURCE_STATE" = "shut off" ] || fail "source VM $VM_NAME is not shut off (state: $SOURCE_STATE)"
echo "  - checking that the source image exists"
source_ssh "test -f '$SOURCE_IMAGE'"
echo "  - checking qcow2 integrity (this may take a while for a large image)"
source_ssh "command -v qemu-img >/dev/null && qemu-img check -f qcow2 '$SOURCE_IMAGE'"

SOURCE_SIZE=$(source_ssh "wc -c < '$SOURCE_IMAGE'")
case "$SOURCE_SIZE" in
  ''|*[!0-9]*) fail "could not determine source image size" ;;
esac

echo "[2/6] Checking target libvirt, storage, and network"
target_ssh "
  set -eu
  command -v qemu-img >/dev/null
  virsh -c qemu:///system uri >/dev/null
  ip link show dev '$BRIDGE' >/dev/null
  test -d '$TARGET_BASE'
  test -w '$TARGET_BASE'
  test ! -e '$TARGET_DIR'
  ! LC_ALL=C virsh -c qemu:///system dominfo '$VM_NAME' >/dev/null 2>&1
  ! ss -ltn \"sport = :$VNC_PORT\" | grep -q LISTEN
  virsh -c qemu:///system domcapabilities | grep -F '<machine>$TARGET_MACHINE</machine>' >/dev/null
"

TARGET_AVAILABLE_KIB=$(target_ssh "df -Pk '$TARGET_BASE' | awk 'NR == 2 { print \$4 }'")
case "$TARGET_AVAILABLE_KIB" in
  ''|*[!0-9]*) fail "could not determine target free space" ;;
esac

TARGET_AVAILABLE=$((TARGET_AVAILABLE_KIB * 1024))
HEADROOM=$((5 * 1024 * 1024 * 1024))
if [ "$TARGET_AVAILABLE" -le $((SOURCE_SIZE + HEADROOM)) ]; then
  fail "target needs the full source size plus 5 GiB of free space"
fi

echo "Source image: $SOURCE_IMAGE ($SOURCE_SIZE bytes)"
echo "Target image: $TARGET_IMAGE"
echo "Target free space: $TARGET_AVAILABLE bytes"

if [ "$EXECUTE" -ne 1 ]; then
  echo "Preflight passed. Re-run with --execute to copy and define $VM_NAME."
  exit 0
fi

TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/win7-ui-migration.XXXXXX")
chmod 700 "$TMP_DIR"
SOURCE_XML="$TMP_DIR/$VM_NAME.xml"
TARGET_XML_LOCAL="$TMP_DIR/$VM_NAME.target.xml"

echo "[3/6] Exporting and adapting the domain definition"
source_ssh "LC_ALL=C virsh dumpxml '$VM_NAME'" > "$SOURCE_XML"
sed \
  -e '/<emulator>\/usr\/libexec\/qemu-kvm<\/emulator>/d' \
  -e "s#machine='pc-i440fx-rhel7.0.0'#machine='$TARGET_MACHINE'#" \
  -e "s#<source file='$SOURCE_IMAGE'/>#<source file='$TARGET_IMAGE'/>#" \
  "$SOURCE_XML" > "$TARGET_XML_LOCAL"

grep -F "machine='$TARGET_MACHINE'" "$TARGET_XML_LOCAL" >/dev/null || fail "could not update the machine type in domain XML"
grep -F "<source file='$TARGET_IMAGE'/>" "$TARGET_XML_LOCAL" >/dev/null || fail "could not update the disk path in domain XML"

echo "[4/6] Copying the image through the operator workstation"
target_ssh "install -d -m 0750 '$TARGET_DIR'"
if ! scp -3 -p -P "$SOURCE_PORT" \
  -o BatchMode=yes \
  "$SOURCE_HOST:$SOURCE_IMAGE" \
  "$TARGET_HOST:$TARGET_IMAGE"; then
  echo "Copy failed. The partial target directory was intentionally retained for inspection." >&2
  exit 1
fi

echo "[5/6] Verifying the copied image"
SOURCE_SHA256=$(source_ssh "sha256sum '$SOURCE_IMAGE' | awk '{print \$1}'")
TARGET_SHA256=$(target_ssh "sha256sum '$TARGET_IMAGE' | awk '{print \$1}'")
[ "$SOURCE_SHA256" = "$TARGET_SHA256" ] || fail "SHA-256 mismatch: source=$SOURCE_SHA256 target=$TARGET_SHA256"
target_ssh "
  set -eu
  chmod 0644 '$TARGET_IMAGE'
  qemu-img check -f qcow2 '$TARGET_IMAGE'
"

echo "[6/6] Defining the target VM"
target_ssh "! LC_ALL=C virsh -c qemu:///system dominfo '$VM_NAME' >/dev/null 2>&1"
scp -P "$TARGET_PORT" -o BatchMode=yes "$TARGET_XML_LOCAL" "$TARGET_HOST:$TARGET_XML"
target_ssh "
  set -eu
  ! ss -ltn \"sport = :$VNC_PORT\" | grep -q LISTEN
  LC_ALL=C virsh -c qemu:///system define '$TARGET_XML'
  LC_ALL=C virsh -c qemu:///system dominfo '$VM_NAME'
"

if [ "$START_VM" -eq 1 ]; then
  echo "Starting $VM_NAME on $TARGET_HOST"
  target_ssh "
    set -eu
    LC_ALL=C virsh -c qemu:///system start '$VM_NAME'
    sleep 5
    test \"\$(LC_ALL=C virsh -c qemu:///system domstate '$VM_NAME')\" = running
    ss -ltn \"sport = :$VNC_PORT\" | grep -q LISTEN
  "
fi

echo "Migration script completed. The source VM and source image were not changed."
