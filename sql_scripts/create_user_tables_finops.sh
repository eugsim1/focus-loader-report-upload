#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$script_dir/.env" ]] && { set -a; source "$script_dir/.env"; set +a; }

: "${TNS_ADMIN:?Set TNS_ADMIN to the extracted wallet directory}"
: "${DB_ADMIN_PASSWORD:?Set DB_ADMIN_PASSWORD}"
: "${DB_TNS_ALIAS:?Set DB_TNS_ALIAS}"
: "${TARGET_SCHEMA:?Set TARGET_SCHEMA}"
: "${TARGET_SCHEMA_PASSWORD:?Set TARGET_SCHEMA_PASSWORD}"

exec "$script_dir/create_tables.sh"
