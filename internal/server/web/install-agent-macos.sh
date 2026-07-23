#!/bin/sh
set -eu

PATH="/usr/bin:/bin:/usr/sbin:/sbin"
export PATH

INSTALL_DIR="/Library/Application Support/ServerStatus"
AGENT="$INSTALL_DIR/server-status-agent"
CONFIG="$INSTALL_DIR/agent.env"
LOG_DIR="/Library/Logs/ServerStatus"
STDOUT_LOG="$LOG_DIR/agent.log"
STDERR_LOG="$LOG_DIR/agent.error.log"
PLIST="/Library/LaunchDaemons/com.guohai.server-status-agent.plist"
SERVICE="system/com.guohai.server-status-agent"

fail() {
  echo "server-status-agent macOS installer: $*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

[ "$(id -u)" -eq 0 ] || fail "run this installer through sudo or as root"
[ "$(uname -s)" = "Darwin" ] || fail "only macOS is supported"

for command_name in curl shasum awk sed grep mktemp launchctl zsh; do
  require_command "$command_name"
done

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

asset="server-status-agent-macos"
temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/server-status-macos-installer.XXXXXX")
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
actual_checksum=$(shasum -a 256 "$temporary_directory/$asset" | awk '{ print $1 }')
[ "$actual_checksum" = "$expected_checksum" ] || fail "SHA-256 verification failed for $asset"
/bin/zsh -n "$temporary_directory/$asset" || fail "downloaded Agent has invalid zsh syntax"

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
  printf 'SERVER_STATUS_INTERVAL_SECONDS=60\n'
  printf 'SERVER_STATUS_LABELS=%s\n' "$(shell_quote "$labels")"
} > "$config_new"
chmod 0600 "$config_new"

plist_new="$temporary_directory/com.guohai.server-status-agent.plist"
cat > "$plist_new" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.guohai.server-status-agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Library/Application Support/ServerStatus/server-status-agent</string>
    <string>run</string>
    <string>/Library/Application Support/ServerStatus/agent.env</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ThrottleInterval</key>
  <integer>10</integer>
  <key>StandardOutPath</key>
  <string>/Library/Logs/ServerStatus/agent.log</string>
  <key>StandardErrorPath</key>
  <string>/Library/Logs/ServerStatus/agent.error.log</string>
</dict>
</plist>
PLIST
/usr/bin/plutil -lint "$plist_new" >/dev/null || fail "generated LaunchDaemon plist is invalid"

mkdir -p "$INSTALL_DIR" "$LOG_DIR"
chown root:wheel "$INSTALL_DIR" "$LOG_DIR"
chmod 0750 "$INSTALL_DIR" "$LOG_DIR"

agent_new="$INSTALL_DIR/server-status-agent.new"
config_target_new="$INSTALL_DIR/agent.env.new"
plist_target_new="$PLIST.new"
cp "$temporary_directory/$asset" "$agent_new"
cp "$config_new" "$config_target_new"
cp "$plist_new" "$plist_target_new"
chown root:wheel "$agent_new" "$config_target_new" "$plist_target_new"
chmod 0755 "$agent_new"
chmod 0600 "$config_target_new"
chmod 0644 "$plist_target_new"

/bin/launchctl bootout "$SERVICE" 2>/dev/null || true
mv -f "$agent_new" "$AGENT"
mv -f "$config_target_new" "$CONFIG"
mv -f "$plist_target_new" "$PLIST"

if [ -r "$STDOUT_LOG" ]; then
  log_bytes=$(wc -c < "$STDOUT_LOG")
else
  log_bytes=0
fi
start_byte=$((log_bytes + 1))

/bin/launchctl bootstrap system "$PLIST" || fail "cannot bootstrap LaunchDaemon"
/bin/launchctl enable "$SERVICE" || true
/bin/launchctl kickstart -k "$SERVICE" || fail "cannot start LaunchDaemon"

attempt=0
while [ "$attempt" -lt 30 ]; do
  if tail -c +"$start_byte" "$STDOUT_LOG" 2>/dev/null | grep -q 'level=info report accepted'; then
    installed_version=$("$AGENT" --version 2>/dev/null || printf 'unknown')
    echo "macOS Agent installed successfully: $installed_version"
    exit 0
  fi
  sleep 1
  attempt=$((attempt + 1))
done

echo "Agent started, but the central service did not accept a report within 30 seconds." >&2
tail -n 30 "$STDOUT_LOG" >&2 2>/dev/null || true
tail -n 30 "$STDERR_LOG" >&2 2>/dev/null || true
exit 1
