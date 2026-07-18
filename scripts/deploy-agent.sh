#!/bin/sh
set -eu

TARGET="${SERVER_STATUS_AGENT_TARGET:-gydev@10.12.54.169}"
CENTRAL_TARGET="${SERVER_STATUS_CENTRAL_TARGET:-gydev@10.12.54.200}"
CENTRAL_REMOTE_DIR="${SERVER_STATUS_CENTRAL_DIR:-server-status-central}"
CENTRAL_URL="${SERVER_STATUS_URL:-http://10.12.54.200:8080}"
ENV_FILE="${SERVER_STATUS_AGENT_ENV_FILE:-}"
BINARY_OVERRIDE="${SERVER_STATUS_AGENT_BINARY:-}"
BINARY="${BINARY_OVERRIDE:-dist/server-status-agent-linux-amd64}"
LABEL_ENVIRONMENT="${SERVER_STATUS_AGENT_ENVIRONMENT:-production}"
TMP_DIR=""

cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to create and validate registration JSON" >&2
  exit 1
fi

if [ -z "$BINARY_OVERRIDE" ]; then
  make build-agent-linux
elif [ ! -x "$BINARY" ]; then
  echo "SERVER_STATUS_AGENT_BINARY is not executable: $BINARY" >&2
  exit 1
fi

echo "[1/6] Preparing $TARGET"
ssh "$TARGET" 'mkdir -p "$HOME/.local/lib/server-status-agent" "$HOME/.config/server-status-agent" "$HOME/.local/state/server-status-agent"'
scp "$BINARY" "$TARGET:.local/lib/server-status-agent/server-status-agent.new"
scp deploy/run-agent.sh "$TARGET:.local/lib/server-status-agent/run-agent.sh"

if [ -z "$ENV_FILE" ]; then
  echo "[2/6] Reading node identity and checking the central API"
  NODE_HOSTNAME=$(ssh "$TARGET" 'hostname')
  NODE_OS_NAME=$(ssh "$TARGET" '. /etc/os-release; printf "%s" "${NAME:-Linux}"')
  NODE_OS_VERSION=$(ssh "$TARGET" '. /etc/os-release; printf "%s" "${VERSION_ID:-unknown}"')
  NODE_ARCHITECTURE=$(ssh "$TARGET" 'uname -m')
  NODE_ADDRESS=${TARGET#*@}
  EXISTING_AGENT_ID=$(ssh "$TARGET" 'sed -n "s/^SERVER_STATUS_AGENT_ID=//p" "$HOME/.config/server-status-agent/env" 2>/dev/null | head -1 || true')
  ssh "$TARGET" "curl -fsS --max-time 10 '$CENTRAL_URL/readyz' >/dev/null"

  REGISTRATION_PAYLOAD=$(python3 -c '
import json, sys
hostname, os_name, os_version, architecture, address, environment, agent_id = sys.argv[1:]
payload = {
    "hostname": hostname,
    "display_name": address,
    "os_name": os_name,
    "os_version": os_version,
    "architecture": architecture,
    "agent_version": "0.1.0",
    "labels": {"environment": environment, "address": address},
}
if agent_id:
    payload["agent_id"] = agent_id
print(json.dumps(payload, separators=(",", ":")))
' "$NODE_HOSTNAME" "$NODE_OS_NAME" "$NODE_OS_VERSION" "$NODE_ARCHITECTURE" "$NODE_ADDRESS" "$LABEL_ENVIRONMENT" "$EXISTING_AGENT_ID")

  echo "[3/6] Registering the node through $CENTRAL_TARGET"
  REGISTRATION_RESPONSE=$(printf '%s' "$REGISTRATION_PAYLOAD" | ssh "$CENTRAL_TARGET" "
    set -a
    . \"\$HOME/$CENTRAL_REMOTE_DIR/.env\"
    set +a
    curl -fsS --max-time 15 -X POST http://127.0.0.1:8080/api/v1/admin/nodes \\
      -H \"Authorization: Bearer \$SERVER_STATUS_ADMIN_TOKEN\" \\
      -H 'Content-Type: application/json' \\
      --data-binary @-
  ")

  TMP_DIR=$(mktemp -d "${TMPDIR:-/tmp}/server-status-agent.XXXXXX")
  chmod 700 "$TMP_DIR"
  printf '%s' "$REGISTRATION_RESPONSE" > "$TMP_DIR/credentials.json"
  chmod 600 "$TMP_DIR/credentials.json"
  ENV_FILE="$TMP_DIR/agent.env"
  python3 -c '
import json, pathlib, shlex, sys
credentials_path, output_path, central_url, environment, address = sys.argv[1:]
credentials = json.loads(pathlib.Path(credentials_path).read_text())
for key in ("agent_id", "token"):
    if not isinstance(credentials.get(key), str) or not credentials[key]:
        raise SystemExit("registration response is missing " + key)
labels = json.dumps({"environment": environment, "address": address}, separators=(",", ":"))
lines = [
    "SERVER_STATUS_URL=" + central_url,
    "SERVER_STATUS_AGENT_ID=" + credentials["agent_id"],
    "SERVER_STATUS_TOKEN=" + credentials["token"],
    "SERVER_STATUS_INTERVAL=1m",
    "SERVER_STATUS_LABELS=" + shlex.quote(labels),
    "",
]
path = pathlib.Path(output_path)
path.write_text("\n".join(lines))
path.chmod(0o600)
' "$TMP_DIR/credentials.json" "$ENV_FILE" "$CENTRAL_URL" "$LABEL_ENVIRONMENT" "$NODE_ADDRESS"
else
  echo "[2/6] Using the supplied Agent environment file"
  if [ ! -r "$ENV_FILE" ]; then
    echo "SERVER_STATUS_AGENT_ENV_FILE is not readable: $ENV_FILE" >&2
    exit 1
  fi
  echo "[3/6] Skipping automatic registration"
fi

echo "[4/6] Installing the binary and protected configuration"
scp "$ENV_FILE" "$TARGET:.config/server-status-agent/env.new"
ssh "$TARGET" '
  chmod 700 "$HOME/.local/lib/server-status-agent/server-status-agent.new" "$HOME/.local/lib/server-status-agent/run-agent.sh"
  chmod 600 "$HOME/.config/server-status-agent/env.new"
  mv -f "$HOME/.local/lib/server-status-agent/server-status-agent.new" "$HOME/.local/lib/server-status-agent/server-status-agent"
  mv -f "$HOME/.config/server-status-agent/env.new" "$HOME/.config/server-status-agent/env"
'

echo "[5/6] Installing the reboot entry and watchdog"
ssh "$TARGET" '(
  crontab -l 2>/dev/null | grep -v "server-status-agent/run-agent.sh" || true
  echo "@reboot $HOME/.local/lib/server-status-agent/run-agent.sh >>$HOME/.local/state/server-status-agent/agent.log 2>&1 &"
  echo "*/5 * * * * pgrep -u $(id -u) -f \"[s]erver-status-agent/server-status-agent\" >/dev/null || nohup $HOME/.local/lib/server-status-agent/run-agent.sh >>$HOME/.local/state/server-status-agent/agent.log 2>&1 &"
) | crontab -'

LOG_BYTES=$(ssh "$TARGET" 'wc -c < "$HOME/.local/state/server-status-agent/agent.log" 2>/dev/null || printf "0"')
ssh "$TARGET" 'pkill -u "$(id -u)" -f "[s]erver-status-agent/server-status-agent" 2>/dev/null || true; nohup "$HOME/.local/lib/server-status-agent/run-agent.sh" >>"$HOME/.local/state/server-status-agent/agent.log" 2>&1 </dev/null &'

echo "[6/6] Waiting for the first accepted report"
ssh "$TARGET" sh -s -- "$LOG_BYTES" <<'REMOTE_VERIFY'
start_byte=$(($1 + 1))
attempt=0
while [ "$attempt" -lt 20 ]; do
  if tail -c +"$start_byte" "$HOME/.local/state/server-status-agent/agent.log" 2>/dev/null | grep -q '"msg":"report accepted"'; then
    echo "Agent deployment verified: the central service accepted the first report."
    exit 0
  fi
  sleep 1
  attempt=$((attempt + 1))
done

echo "Agent started but no accepted report was observed within 20 seconds." >&2
tail -c +"$start_byte" "$HOME/.local/state/server-status-agent/agent.log" >&2 || true
exit 1
REMOTE_VERIFY
