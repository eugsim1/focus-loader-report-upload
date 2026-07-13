#!/usr/bin/env bash
set -Eeuo pipefail

ENV_FILE=${FOCUS_LOADER_ENV:-/etc/focus-loader/focus-loader.env}
if [[ ! -r "$ENV_FILE" ]]; then
  echo "ERROR: configuration is not readable: $ENV_FILE" >&2
  exit 2
fi

# shellcheck disable=SC1090
source "$ENV_FILE"

: "${APP_DIR:?APP_DIR is required}"
: "${DATA_DIR:?DATA_DIR is required}"
: "${LOG_DIR:?LOG_DIR is required}"
: "${MODE:?MODE is required}"
: "${DB_USER:?DB_USER is required}"
: "${DB_CONNECT:?DB_CONNECT is required}"
: "${SOURCE_NAMESPACE:?SOURCE_NAMESPACE is required}"

BIN="$APP_DIR/focus-loader-report-upload"
[[ -x "$BIN" ]] || { echo "ERROR: binary is not executable: $BIN" >&2; exit 2; }
[[ -r "$APP_DIR/focus.conf" ]] || { echo "ERROR: missing $APP_DIR/focus.conf" >&2; exit 2; }

mkdir -p "$DATA_DIR" "$LOG_DIR" "$(dirname "${LOCK_FILE:-$DATA_DIR/focus-loader.lock}")"
exec 9>"${LOCK_FILE:-$DATA_DIR/focus-loader.lock}"
if ! flock -n 9; then
  echo "INFO: another focus-loader run is active; skipping this schedule"
  exit 0
fi

export TNS_ADMIN=${TNS_ADMIN:-}
export LD_LIBRARY_PATH="${ORACLE_CLIENT_LIB:-}${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export PATH="${ORACLE_CLIENT_BIN:-}:$PATH"

args=(
  -du "$DB_USER"
  -dn "$DB_CONNECT"
  -ns "$SOURCE_NAMESPACE"
  -workers "${WORKERS:-1}"
  -state-file "${LOAD_STATE_FILE:-$DATA_DIR/processed_files.jsonl}"
)

case "${OCI_AUTH_MODE:-instance_principal}" in
  instance_principal)
    args+=(-ip)
    ;;
  config_file)
    : "${OCI_CONFIG_FILE:?OCI_CONFIG_FILE is required for config_file auth}"
    args+=(-c "$OCI_CONFIG_FILE" -t "${OCI_PROFILE:-DEFAULT}")
    ;;
  *)
    echo "ERROR: OCI_AUTH_MODE must be instance_principal or config_file" >&2
    exit 2
    ;;
esac

if [[ -n "${DB_SECRET_ID:-}" ]]; then
  args+=(-ds "$DB_SECRET_ID")
  [[ -n "${DB_SECRET_PROFILE:-}" ]] && args+=(-dst "$DB_SECRET_PROFILE")
elif [[ -n "${DB_PASSWORD_FILE:-}" ]]; then
  [[ -r "$DB_PASSWORD_FILE" ]] || { echo "ERROR: DB_PASSWORD_FILE is not readable" >&2; exit 2; }
  IFS= read -r db_password < "$DB_PASSWORD_FILE"
  [[ -n "$db_password" ]] || { echo "ERROR: DB_PASSWORD_FILE is empty" >&2; exit 2; }
  args+=(-dp "$db_password")
else
  echo "ERROR: set DB_SECRET_ID (recommended) or DB_PASSWORD_FILE" >&2
  exit 2
fi

[[ -n "${SOURCE_BUCKET:-}" ]] && args+=(-bn "$SOURCE_BUCKET")
[[ -n "${MINIMUM_FILE_DATE:-}" ]] && args+=(-d "$MINIMUM_FILE_DATE")
[[ "${KEEP_WORK_FILES:-false}" == true ]] && args+=(-keep-work-files)
[[ "${SKIP_TAGS:-false}" == true ]] && args+=(-skip-tags)
[[ "${VERBOSE:-false}" == true ]] && args+=(-verbose)

case "$MODE" in
  upload-only|upload-and-load)
    : "${DESTINATION_BUCKET:?DESTINATION_BUCKET is required for $MODE}"
    args+=(
      -upload-reports
      -report-upload-bucket "$DESTINATION_BUCKET"
      -upload-state-file "${UPLOAD_STATE_FILE:-$DATA_DIR/uploaded_files.jsonl}"
      -latest-upload-file "${LATEST_UPLOAD_FILE:-$DATA_DIR/latest_focus_file_upload.csv}"
      -focus-upload-results-file "${UPLOAD_RESULTS_FILE:-$DATA_DIR/focus_file_upload_results.csv}"
    )
    [[ -n "${DESTINATION_NAMESPACE:-}" ]] && args+=(-report-upload-namespace "$DESTINATION_NAMESPACE")
    [[ -n "${DESTINATION_REGION:-}" ]] && args+=(-report-upload-region "$DESTINATION_REGION")
    [[ -n "${DESTINATION_PREFIX:-}" ]] && args+=(-report-upload-prefix "$DESTINATION_PREFIX")
    [[ "$MODE" == upload-and-load ]] && args+=(-load-after-upload)
    ;;
  load)
    ;;
  preload-report)
    args+=(-preload-report -preload-report-file "$DATA_DIR/preload_report.csv")
    ;;
  *)
    echo "ERROR: MODE must be upload-only, load, upload-and-load, or preload-report" >&2
    exit 2
    ;;
esac

cd "$APP_DIR"
echo "INFO: starting focus-loader mode=$MODE at $(date --iso-8601=seconds)"
set +e
"$BIN" "${args[@]}"
status=$?
set -e
echo "INFO: focus-loader ended status=$status at $(date --iso-8601=seconds)"
exit "$status"
