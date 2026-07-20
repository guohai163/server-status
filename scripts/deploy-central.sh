#!/bin/sh
set -eu

TARGET="${SERVER_STATUS_CENTRAL_TARGET:-gydev@10.12.54.200}"
REMOTE_DIR="${SERVER_STATUS_CENTRAL_DIR:-server-status-central}"
ENV_FILE="${SERVER_STATUS_CENTRAL_ENV_FILE:-}"

if [ -z "$ENV_FILE" ] || [ ! -r "$ENV_FILE" ]; then
  echo "set SERVER_STATUS_CENTRAL_ENV_FILE to a readable central .env file" >&2
  exit 1
fi

ssh "$TARGET" "mkdir -p '$REMOTE_DIR'"
scp compose.yaml "$TARGET:$REMOTE_DIR/"
scp "$ENV_FILE" "$TARGET:$REMOTE_DIR/.env"
ssh "$TARGET" "chmod 600 '$REMOTE_DIR/.env' && cd '$REMOTE_DIR' && docker compose pull && docker compose up -d --remove-orphans"
