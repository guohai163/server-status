#!/bin/sh
set -eu

CONFIG_FILE="${SERVER_STATUS_AGENT_ENV:-$HOME/.config/server-status-agent/env}"
if [ ! -r "$CONFIG_FILE" ]; then
  echo "agent environment file is not readable: $CONFIG_FILE" >&2
  exit 1
fi

set -a
. "$CONFIG_FILE"
set +a

exec "$HOME/.local/lib/server-status-agent/server-status-agent"
