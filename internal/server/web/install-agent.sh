#!/bin/sh
set -eu

INSTALL_DIR="/opt/server-agent"
BINARY="$INSTALL_DIR/server-status-agent"
RUNNER="$INSTALL_DIR/run-agent.sh"
CONFIG="$INSTALL_DIR/agent.env"
LOG_FILE="/var/log/server-status-agent.log"
CRON_MARKER="# server-status-agent-managed"

fail() {
  echo "server-status-agent installer: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

[ "$(id -u)" -eq 0 ] || fail "run this installer through sudo or as root"
[ "$(uname -s)" = "Linux" ] || fail "only Linux is supported"

for command_name in curl crontab sha256sum awk sed grep pgrep pkill nohup mktemp tail; do
  require_command "$command_name"
done

case "$(uname -m)" in
  x86_64|amd64) architecture="amd64" ;;
  aarch64|arm64) architecture="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

SERVER_STATUS_URL=${SERVER_STATUS_URL:-}
SERVER_STATUS_AGENT_ID=${SERVER_STATUS_AGENT_ID:-}
SERVER_STATUS_TOKEN=${SERVER_STATUS_TOKEN:-}
SERVER_STATUS_AGENT_ENVIRONMENT=${SERVER_STATUS_AGENT_ENVIRONMENT:-}
SERVER_STATUS_AGENT_VERSION=${SERVER_STATUS_AGENT_VERSION:-}

[ -n "$SERVER_STATUS_URL" ] || fail "SERVER_STATUS_URL is required"
[ -n "$SERVER_STATUS_AGENT_ID" ] || fail "SERVER_STATUS_AGENT_ID is required"
[ -n "$SERVER_STATUS_TOKEN" ] || fail "SERVER_STATUS_TOKEN is required"

if [ -n "$SERVER_STATUS_AGENT_ENVIRONMENT" ] &&
   ! printf '%s' "$SERVER_STATUS_AGENT_ENVIRONMENT" | grep -Eq '^[A-Za-z0-9._-]{1,64}$'; then
  fail "SERVER_STATUS_AGENT_ENVIRONMENT may contain only letters, numbers, dot, underscore, and hyphen"
fi

if [ -n "$SERVER_STATUS_AGENT_VERSION" ]; then
  version=${SERVER_STATUS_AGENT_VERSION#v}
  if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$'; then
    fail "SERVER_STATUS_AGENT_VERSION must be a semantic version"
  fi
  release_base="${SERVER_STATUS_URL%/}/agent/releases/v$version"
else
  release_base="${SERVER_STATUS_URL%/}/agent/releases/latest"
fi

asset="server-status-agent-linux-$architecture"
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/server-status-agent.XXXXXX")
cleanup() {
  rm -rf "$temporary_directory"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

echo "Downloading $asset through the central release cache"
curl -fsSL --connect-timeout 15 --max-time 300 "$release_base/$asset" \
  -o "$temporary_directory/$asset"
curl -fsSL --connect-timeout 15 --max-time 300 "$release_base/checksums.txt" \
  -o "$temporary_directory/checksums.txt"

expected_checksum=$(awk -v asset="$asset" '$2 == asset { print $1; exit }' "$temporary_directory/checksums.txt")
[ -n "$expected_checksum" ] || fail "checksums.txt does not contain $asset"
actual_checksum=$(sha256sum "$temporary_directory/$asset" | awk '{print $1}')
[ "$actual_checksum" = "$expected_checksum" ] || fail "SHA-256 verification failed for $asset"

mkdir -p "$INSTALL_DIR"
chmod 0755 "$INSTALL_DIR"

shell_quote() {
  escaped=$(printf '%s' "$1" | sed "s/'/'\\\\''/g")
  printf "'%s'" "$escaped"
}

if [ -n "$SERVER_STATUS_AGENT_ENVIRONMENT" ]; then
  labels="{\"environment\":\"$SERVER_STATUS_AGENT_ENVIRONMENT\"}"
else
  labels="{}"
fi

config_new="$temporary_directory/agent.env"
{
  printf 'SERVER_STATUS_URL=%s\n' "$(shell_quote "$SERVER_STATUS_URL")"
  printf 'SERVER_STATUS_AGENT_ID=%s\n' "$(shell_quote "$SERVER_STATUS_AGENT_ID")"
  printf 'SERVER_STATUS_TOKEN=%s\n' "$(shell_quote "$SERVER_STATUS_TOKEN")"
  printf 'SERVER_STATUS_INTERVAL=1m\n'
  printf 'SERVER_STATUS_LABELS=%s\n' "$(shell_quote "$labels")"
  printf 'SERVER_STATUS_LOCK_FILE=/var/run/server-status-agent.lock\n'
} > "$config_new"
chmod 0600 "$config_new"

runner_new="$temporary_directory/run-agent.sh"
cat > "$runner_new" <<'RUNNER'
#!/bin/sh
set -eu

set -a
. /opt/server-agent/agent.env
set +a

exec /opt/server-agent/server-status-agent
RUNNER
chmod 0755 "$runner_new"

binary_new="$INSTALL_DIR/server-status-agent.new"
runner_target_new="$INSTALL_DIR/run-agent.sh.new"
config_target_new="$INSTALL_DIR/agent.env.new"
cp "$temporary_directory/$asset" "$binary_new"
cp "$runner_new" "$runner_target_new"
cp "$config_new" "$config_target_new"
chmod 0755 "$binary_new" "$runner_target_new"
chmod 0600 "$config_target_new"
mv -f "$binary_new" "$BINARY"
mv -f "$runner_target_new" "$RUNNER"
mv -f "$config_target_new" "$CONFIG"

cron_new="$temporary_directory/crontab"
(crontab -l 2>/dev/null || true) | grep -Fv "$CRON_MARKER" > "$cron_new" || true
printf '%s\n' "@reboot nohup $RUNNER >>$LOG_FILE 2>&1 </dev/null & $CRON_MARKER" >> "$cron_new"
printf '%s\n' "*/5 * * * * pgrep -f '[/]opt/server-agent/server-status-agent' >/dev/null || nohup $RUNNER >>$LOG_FILE 2>&1 </dev/null & $CRON_MARKER" >> "$cron_new"
crontab "$cron_new"

if [ -r "$LOG_FILE" ]; then
  log_bytes=$(wc -c < "$LOG_FILE")
else
  log_bytes=0
fi
start_byte=$((log_bytes + 1))

pkill -f '[/]opt/server-agent/server-status-agent' 2>/dev/null || true
stop_attempt=0
while pgrep -f '[/]opt/server-agent/server-status-agent' >/dev/null && [ "$stop_attempt" -lt 5 ]; do
  sleep 1
  stop_attempt=$((stop_attempt + 1))
done
if pgrep -f '[/]opt/server-agent/server-status-agent' >/dev/null; then
  fail "the previous Agent process did not stop"
fi
nohup "$RUNNER" >> "$LOG_FILE" 2>&1 </dev/null &

attempt=0
while [ "$attempt" -lt 20 ]; do
  if tail -c +"$start_byte" "$LOG_FILE" 2>/dev/null | grep -q '"msg":"report accepted"'; then
    installed_version=$($BINARY --version 2>/dev/null || printf 'unknown')
    echo "Agent installed successfully: $installed_version"
    exit 0
  fi
  sleep 1
  attempt=$((attempt + 1))
done

echo "Agent started, but the central service did not accept a report within 20 seconds." >&2
tail -c +"$start_byte" "$LOG_FILE" >&2 || true
exit 1
