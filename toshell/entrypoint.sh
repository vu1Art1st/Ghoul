#!/bin/sh
set -eu

CONFIG_DIR=/opt/toshell/configs
CONFIG_FILE="${CONFIG_DIR}/server.yaml"
REVISION_FILE="${CONFIG_DIR}/.secret-revision"
SECRET_DIR=/run/secrets

read_secret() {
	path="$1"
	if [ ! -s "$path" ]; then
		echo "[toshell] missing secret: $path" >&2
		exit 1
	fi
	tr -d '\r\n' < "$path"
}

mkdir -p "$CONFIG_DIR" /opt/toshell/data /opt/toshell/implants /opt/toshell/plugins /opt/toshell/logs

revision="$(read_secret "${SECRET_DIR}/toshell_secret_revision")"
current_revision=""
if [ -s "$REVISION_FILE" ]; then
	current_revision="$(cat "$REVISION_FILE")"
fi

if [ ! -s "$CONFIG_FILE" ] || [ "$current_revision" != "$revision" ]; then
	export TOSHELL_API_KEY="$(read_secret "${SECRET_DIR}/toshell_api_key")"
	export TOSHELL_MCP_TOKEN="$(read_secret "${SECRET_DIR}/toshell_mcp_token")"
	export TOSHELL_ADMIN_PASSWORD="$(read_secret "${SECRET_DIR}/toshell_admin_password")"
	export TOSHELL_ADMIN_USERNAME="${TOSHELL_ADMIN_USERNAME:-admin}"
	export TOSHELL_PUBLIC_HOST="${TOSHELL_PUBLIC_HOST:-127.0.0.1}"

	tmp="${CONFIG_FILE}.tmp"
	envsubst < /opt/toshell/server.yaml.template > "$tmp"
	chmod 600 "$tmp"
	mv "$tmp" "$CONFIG_FILE"
	printf '%s' "$revision" > "$REVISION_FILE"
	echo "[toshell] generated ${CONFIG_FILE} from tracked template"
fi

exec /opt/toshell/toserver -config "$CONFIG_FILE"
