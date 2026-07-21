#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  cat <<'EOF'
Usage:
  ./initial_load.sh

Required environment variables:
  TNS_ADMIN                 Directory containing tnsnames.ora.
  DB_ADMIN_PASSWORD         ADMIN password used by the schema deployment.
  TARGET_SCHEMA_PASSWORD    Password assigned to TARGET_SCHEMA.

Optional environment variables:
  DB_TNS_ALIAS              TNS alias. When empty, the first *_high alias is
                            discovered from $TNS_ADMIN/tnsnames.ora.
  TARGET_SCHEMA             Target database schema (default: FOCUS_GIT1).
  DB_PASSWORD               Loader database password (default:
                            TARGET_SCHEMA_PASSWORD).
  OCI_CONFIG_FILE           OCI CLI configuration (default: $HOME/.oci/config).
  OCI_PROFILE               OCI profile (default: DEFAULT).
  OCI_NAMESPACE             Object Storage namespace (default: bling).
  MINIMUM_DATE              Earliest billing date, YYYY-MM-DD
                            (default: 2026-03-01).
  WORKERS                   Parallel workers (default: 5).
  TAG_SPECIAL_1             Default: Oracle-Tags.CreatedBy
  TAG_SPECIAL_2             Default: CCA_Basic_Tag.email
  TAG_SPECIAL_3             Default: Oracle_Tags.CreatedBy
  TAG_SPECIAL_4             Default: Oracle_Tags.CreatedOn
  PRELOAD_REPORT            true/false (default: true).
  SKIP_PRELOAD_CONTENT_SCAN true/false (default: true).
  SKIP_TAG_ROWS             true/false (default: true).
  CONTINUE_AFTER_REPORT     true/false (default: true).
  BUILD_SCRIPT              Build script relative to the repository root
                            (default: build-linux.sh).
  LOADER_BINARY             Loader relative to the repository root
                            (default: dist/focus-loader-report-upload-linux-amd64).
  SQL_SCRIPTS_DIR           SQL directory relative to the repository root
                            (default: sql_scripts).
  SCHEMA_DEPLOY_SCRIPT      Schema script filename
                            (default: run_deploy_focus_schema_with_sqlloader_audit.sh).
  TNSNAMES_FILE             Explicit tnsnames.ora path (default:
                            $TNS_ADMIN/tnsnames.ora).

Example:
  export TNS_ADMIN=/home/oracle/adb_wallet
  read -rsp "Database ADMIN password: " DB_ADMIN_PASSWORD; echo
  export DB_ADMIN_PASSWORD
  read -rsp "Target schema password: " TARGET_SCHEMA_PASSWORD; echo
  export TARGET_SCHEMA_PASSWORD
  export TARGET_SCHEMA=FOCUS_GIT1
  export OCI_NAMESPACE=bling
  export MINIMUM_DATE=2026-03-01
  ./initial_load.sh

Show this help:
  ./initial_load.sh --help
EOF
}

fail() {
  printf 'ERROR: %s\n\n' "$1" >&2
  usage >&2
  exit 2
}

on_error() {
  local rc=$?
  printf 'ERROR: initial load failed at line %s (exit code %s).\n' "$1" "$rc" >&2
  exit "$rc"
}
trap 'on_error "$LINENO"' ERR

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  "") ;;
  *) fail "Unknown argument: $1" ;;
esac

[[ $# -le 1 ]] || fail "This script accepts no positional arguments; use environment variables."

[[ -n "${TNS_ADMIN:-}" ]] || fail "TNS_ADMIN is required."
[[ -n "${DB_ADMIN_PASSWORD:-}" ]] || fail "DB_ADMIN_PASSWORD is required."
[[ -n "${TARGET_SCHEMA_PASSWORD:-}" ]] || fail "TARGET_SCHEMA_PASSWORD is required."

TARGET_SCHEMA="${TARGET_SCHEMA:-FOCUS_GIT1}"
DB_PASSWORD="${DB_PASSWORD:-$TARGET_SCHEMA_PASSWORD}"
OCI_CONFIG_FILE="${OCI_CONFIG_FILE:-$HOME/.oci/config}"
OCI_PROFILE="${OCI_PROFILE:-DEFAULT}"
OCI_NAMESPACE="${OCI_NAMESPACE:-bling}"
MINIMUM_DATE="${MINIMUM_DATE:-2026-03-01}"
WORKERS="${WORKERS:-5}"
TAG_SPECIAL_1="${TAG_SPECIAL_1:-Oracle-Tags.CreatedBy}"
TAG_SPECIAL_2="${TAG_SPECIAL_2:-CCA_Basic_Tag.email}"
TAG_SPECIAL_3="${TAG_SPECIAL_3:-Oracle_Tags.CreatedBy}"
TAG_SPECIAL_4="${TAG_SPECIAL_4:-Oracle_Tags.CreatedOn}"
PRELOAD_REPORT="${PRELOAD_REPORT:-true}"
SKIP_PRELOAD_CONTENT_SCAN="${SKIP_PRELOAD_CONTENT_SCAN:-true}"
SKIP_TAG_ROWS="${SKIP_TAG_ROWS:-true}"
CONTINUE_AFTER_REPORT="${CONTINUE_AFTER_REPORT:-true}"
BUILD_SCRIPT="${BUILD_SCRIPT:-build-linux.sh}"
LOADER_BINARY="${LOADER_BINARY:-dist/focus-loader-report-upload-linux-amd64}"
SQL_SCRIPTS_DIR="${SQL_SCRIPTS_DIR:-sql_scripts}"
SCHEMA_DEPLOY_SCRIPT="${SCHEMA_DEPLOY_SCRIPT:-run_deploy_focus_schema_with_sqlloader_audit.sh}"

if [[ "$TNS_ADMIN" == "~/"* ]]; then
  TNS_ADMIN="$HOME/${TNS_ADMIN#"~/"}"
fi
TNSNAMES_FILE="${TNSNAMES_FILE:-$TNS_ADMIN/tnsnames.ora}"

[[ -d "$TNS_ADMIN" ]] || fail "TNS_ADMIN directory does not exist: $TNS_ADMIN"
[[ -r "$TNSNAMES_FILE" ]] || fail "tnsnames.ora is not readable: $TNSNAMES_FILE"
[[ -r "$OCI_CONFIG_FILE" ]] || fail "OCI configuration is not readable: $OCI_CONFIG_FILE"
[[ "$TARGET_SCHEMA" =~ ^[A-Za-z][A-Za-z0-9_$#]*$ ]] || fail "TARGET_SCHEMA is not a valid Oracle identifier: $TARGET_SCHEMA"
[[ "$MINIMUM_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || fail "MINIMUM_DATE must use YYYY-MM-DD format."
[[ "$WORKERS" =~ ^[1-9][0-9]*$ ]] || fail "WORKERS must be a positive integer."

for boolean_name in PRELOAD_REPORT SKIP_PRELOAD_CONTENT_SCAN SKIP_TAG_ROWS CONTINUE_AFTER_REPORT; do
  boolean_value="${!boolean_name}"
  [[ "$boolean_value" == "true" || "$boolean_value" == "false" ]] || \
    fail "$boolean_name must be true or false."
done

if [[ -z "${DB_TNS_ALIAS:-}" ]]; then
  DB_TNS_ALIAS="$({
    awk '
      {
        line=$0
        sub(/^[[:space:]]*/, "", line)
        if (tolower(line) ~ /^[a-z0-9_.-]+_high[[:space:]]*=/) {
          sub(/[[:space:]]*=.*/, "", line)
          print line
          exit
        }
      }
    ' "$TNSNAMES_FILE"
  })"
fi

[[ -n "$DB_TNS_ALIAS" ]] || fail "No *_high TNS alias was found in $TNSNAMES_FILE. Set DB_TNS_ALIAS explicitly."
[[ "$DB_TNS_ALIAS" =~ ^[A-Za-z0-9_.-]+_high$ ]] || fail "DB_TNS_ALIAS must end in _high: $DB_TNS_ALIAS"

build_script_path="$script_dir/$BUILD_SCRIPT"
loader_binary_path="$script_dir/$LOADER_BINARY"
sql_scripts_path="$script_dir/$SQL_SCRIPTS_DIR"
schema_deploy_path="$sql_scripts_path/$SCHEMA_DEPLOY_SCRIPT"

[[ -f "$build_script_path" ]] || fail "Build script was not found: $build_script_path"
[[ -d "$sql_scripts_path" ]] || fail "SQL scripts directory was not found: $sql_scripts_path"
[[ -f "$schema_deploy_path" ]] || fail "Schema deployment script was not found: $schema_deploy_path"

cd "$script_dir"

printf '%s\n' \
  "Repository: $script_dir" \
  "TNS_ADMIN: $TNS_ADMIN" \
  "tnsnames.ora: $TNSNAMES_FILE" \
  "Selected TNS alias: $DB_TNS_ALIAS" \
  "Target schema: $TARGET_SCHEMA" \
  "OCI config: $OCI_CONFIG_FILE" \
  "OCI profile: $OCI_PROFILE" \
  "OCI namespace: $OCI_NAMESPACE" \
  "Minimum date: $MINIMUM_DATE" \
  "Workers: $WORKERS"

chmod u+x "$build_script_path"
"$build_script_path"

[[ -x "$loader_binary_path" ]] || fail "The build did not create an executable loader: $loader_binary_path"
"$loader_binary_path" -version
"$loader_binary_path" -help | grep -- 'preload-content'

while IFS= read -r -d '' shell_script; do
  chmod u+x "$shell_script"
done < <(find "$sql_scripts_path" -maxdepth 1 -type f -name '*.sh' -print0)

(
  cd "$sql_scripts_path"
  export DB_ADMIN_PASSWORD DB_TNS_ALIAS TARGET_SCHEMA TARGET_SCHEMA_PASSWORD
  "./$SCHEMA_DEPLOY_SCRIPT"
)

loader_args=(
  -c "$OCI_CONFIG_FILE"
  -t "$OCI_PROFILE"
  -du "$TARGET_SCHEMA"
  -dn "$DB_TNS_ALIAS"
  -dp "$DB_PASSWORD"
  -ns "$OCI_NAMESPACE"
  -d "$MINIMUM_DATE"
  -workers "$WORKERS"
  -ts1 "$TAG_SPECIAL_1"
  -ts2 "$TAG_SPECIAL_2"
  -ts3 "$TAG_SPECIAL_3"
  -ts4 "$TAG_SPECIAL_4"
)

[[ "$PRELOAD_REPORT" == "true" ]] && loader_args+=(-preload-report)
[[ "$SKIP_PRELOAD_CONTENT_SCAN" == "true" ]] && loader_args+=(-skip-preload-content-scan)
[[ "$SKIP_TAG_ROWS" == "true" ]] && loader_args+=(-skip-tag-rows)
[[ "$CONTINUE_AFTER_REPORT" == "true" ]] && loader_args+=(-continue-after-report)

"$loader_binary_path" "${loader_args[@]}"

printf 'Initial schema deployment and FOCUS load completed successfully.\n'
