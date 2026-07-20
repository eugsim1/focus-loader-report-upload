#!/usr/bin/env bash
set -euo pipefail

#############################################################
# Environment
#############################################################
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$script_dir/.env" ]] && { set -a; source "$script_dir/.env"; set +a; }

CLIENT_HOME=${CLIENT_HOME:-/usr/lib/oracle/current/client64}
export SQLLDR_PATH=${SQLLDR_PATH:-/usr/lib/oracle/current/client64/bin}
export PATH=$PATH:$CLIENT_HOME/bin:$CLIENT_HOME:$SQLLDR_PATH
export CLIENT_HOME PATH

TNS_ADMIN=${TNS_ADMIN:-}
APPDIR=${APPDIR:-$script_dir}
CREDFILE=${CREDFILE:-$APPDIR/config.user}
OUTPUT_FILE=${OUTPUT_FILE:-$APPDIR/focus_tag_keys.csv}

export TNS_ADMIN APPDIR CREDFILE OUTPUT_FILE

cd "$APPDIR"

#############################################################
# Read config safely
#############################################################
DATABASE_USER=${DATABASE_USER:-$(grep "^DATABASE_USER=" "$CREDFILE" | cut -d'=' -f2-)}
DATABASE_NAME=${DATABASE_NAME:-$(grep "^DATABASE_NAME=" "$CREDFILE" | cut -d'=' -f2-)}
DATABASE_SECRET_ID=${DATABASE_SECRET_ID:-$(grep "^DATABASE_SECRET_ID=" "$CREDFILE" | cut -d'=' -f2-)}
PROFILE=${OCI_CLI_PROFILE:-DEFAULT}

#############################################################
# Validate
#############################################################
if [[ -z "${TNS_ADMIN}" || -z "${DATABASE_USER}" || -z "${DATABASE_NAME}" || -z "${DATABASE_SECRET_ID}" ]]; then
    echo "ERROR: TNS_ADMIN, DATABASE_USER, DATABASE_NAME, and DATABASE_SECRET_ID are required"
    exit 1
fi

if [[ ! -r "$TNS_ADMIN/tnsnames.ora" ]]; then
    echo "ERROR: Cannot read $TNS_ADMIN/tnsnames.ora"
    exit 1
fi

if [[ -z "${DATABASE_SECRET_ID}" ]]; then
    echo "ERROR: DATABASE_SECRET_ID is missing"
    exit 1
fi

#############################################################
# Retrieve password from OCI Vault
#############################################################
echo "Retrieving secret from OCI Vault..."

SECRET_BUNDLE=$(oci secrets secret-bundle get \
  --secret-id "$DATABASE_SECRET_ID" \
  --query 'data."secret-bundle-content".content' \
  --raw-output \
  --profile "$PROFILE")

DATABASE_PASS=$(echo "$SECRET_BUNDLE" | base64 --decode)

if [[ -z "$DATABASE_PASS" ]]; then
    echo "ERROR: Failed to retrieve or decode password"
    exit 1
fi
DATABASE_PASS_SQL=${DATABASE_PASS//\"/\"\"}

#############################################################
# Info
#############################################################
echo "----------------------------------------"
echo "Running report:"
echo "  Schema  : ${DATABASE_USER}"
echo "  Service : ${DATABASE_NAME}"
echo "----------------------------------------"

sqlplus -L -S /nolog <<EOF
WHENEVER SQLERROR EXIT SQL.SQLCODE

SET DEFINE OFF
connect ${DATABASE_USER}/"${DATABASE_PASS_SQL}"@"${DATABASE_NAME}"

SET MARKUP CSV ON DELIMITER , QUOTE OFF
SET PAGESIZE 0
SET FEEDBACK OFF
SET HEADING ON
SET TRIMSPOOL ON
SET LINESIZE 32767

SPOOL ${OUTPUT_FILE}

select * from TEMP_OCI_FOCUS_TAG_KEYS;

SPOOL OFF
EXIT;

EOF
