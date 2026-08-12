#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ "${FOCUS_IGNORE_DOTENV:-false}" != "true" && -f "$script_dir/.env" ]]; then
  set -a
  source "$script_dir/.env"
  set +a
fi

if [[ -n "${FOCUS_CREDENTIALS_FD:-}" ]]; then
  credential_fd=${FOCUS_CREDENTIALS_FD}
  if [[ "$credential_fd" != "3" ]]; then
    echo "Invalid protected credential input descriptor" >&2
    exit 1
  fi
  if ! IFS= read -r DB_ADMIN_PASSWORD <&"$credential_fd" \
    || ! IFS= read -r TARGET_SCHEMA_PASSWORD <&"$credential_fd"; then
    echo "Unable to read deployment credentials from the protected input channel" >&2
    exit 1
  fi
  exec 3<&-
  unset FOCUS_CREDENTIALS_FD credential_fd
fi

: "${TNS_ADMIN:?Set TNS_ADMIN to the extracted wallet directory}"
: "${DB_ADMIN_USER:=ADMIN}"
: "${DB_ADMIN_PASSWORD:?Set DB_ADMIN_PASSWORD}"
: "${DB_TNS_ALIAS:?Set DB_TNS_ALIAS}"
: "${TARGET_SCHEMA:?Set TARGET_SCHEMA}"
: "${TARGET_SCHEMA_PASSWORD:?Set TARGET_SCHEMA_PASSWORD}"

export FOCUS_CONFIG_FILE="${FOCUS_CONFIG_FILE:-$script_dir/focus.conf}"
export PARENT_CONFIG_FILE="${PARENT_CONFIG_FILE:-$script_dir/../focus.conf}"
export TNS_ADMIN DB_ADMIN_USER DB_ADMIN_PASSWORD DB_TNS_ALIAS
export TARGET_SCHEMA TARGET_SCHEMA_PASSWORD DROP_EXISTING

deployment_script="$script_dir/deploy_focus_schema_with_sqlloader_audit.sh"
if [[ ! -f "$deployment_script" ]]; then
  echo "Deployment script not found: $deployment_script" >&2
  exit 1
fi
if [[ ! -x "$deployment_script" ]]; then
  chmod u+x "$deployment_script" 2>/dev/null || true
fi
if [[ ! -x "$deployment_script" ]]; then
  echo "Deployment script is not executable: $deployment_script" >&2
  exit 1
fi

echo "Schema deployment wrapper started"
echo "Working directory: $script_dir"
echo "TNS alias: $DB_TNS_ALIAS"
echo "Administrator: $DB_ADMIN_USER"
echo "Target schema: $TARGET_SCHEMA"
echo "Drop existing schema: ${DROP_EXISTING:-true}"
echo "Passwords: [REDACTED]"
exec "$script_dir/deploy_focus_schema_with_sqlloader_audit.sh"
