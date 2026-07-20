#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$script_dir/.env" ]] && { set -a; source "$script_dir/.env"; set +a; }

DB_USER=${DB_ADMIN_USER:-ADMIN}
DB_PASS=${DB_ADMIN_PASSWORD:-}
DB_CONN=${DB_TNS_ALIAS:-}
TARGET_SCHEMA=${TARGET_SCHEMA:-}
: "${TNS_ADMIN:?Set TNS_ADMIN to the extracted wallet directory}"
: "${DB_PASS:?Set DB_ADMIN_PASSWORD}"
: "${DB_CONN:?Set DB_TNS_ALIAS}"
: "${TARGET_SCHEMA:?Set TARGET_SCHEMA}"
[[ "$TARGET_SCHEMA" =~ ^[A-Za-z][A-Za-z0-9_\$#]*$ ]] || {
  echo "Invalid TARGET_SCHEMA: $TARGET_SCHEMA" >&2
  exit 1
}
DB_PASS_SQL=${DB_PASS//\"/\"\"}

sqlplus -L -S /nolog <<EOF
SET DEFINE OFF
CONNECT ${DB_USER}/"${DB_PASS_SQL}"@"${DB_CONN}"
ALTER INDEX ${TARGET_SCHEMA}.TEMP_OCI_FOCUS_PK REBUILD;
EXIT;
EOF
