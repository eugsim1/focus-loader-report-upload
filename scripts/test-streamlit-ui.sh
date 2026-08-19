#!/usr/bin/env bash
set -Eeuo pipefail

FOCUSLOADER_API_URL=${FOCUS_API_URL_FOCUSLOADER:-${FOCUS_API_URL:-http://127.0.0.1:8080}}
ORACLE_API_URL=${FOCUS_API_URL_ORACLE:-http://127.0.0.1:8081}
UI_URL=${FOCUS_UI_URL:-http://127.0.0.1:8501}
PYTHON_BIN=${PYTHON_BIN:-python3}
SQL_SCRIPTS_DIR=${FOCUS_SQL_SCRIPTS_DIR:-/opt/focus-loader/sql_scripts}

work_dir=$(mktemp -d)
cleanup() {
  rm -rf -- "${work_dir}"
}
trap cleanup EXIT

echo "[1/7] focusloader Go backend health"
curl -fsS "${FOCUSLOADER_API_URL}/api/v1/health" -o "${work_dir}/health.json"
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert p["status"] == "ok" and p["version"]' \
  "${work_dir}/health.json"

echo "[2/7] oracle Go backend health"
curl -fsS "${ORACLE_API_URL}/api/v1/health" -o "${work_dir}/oracle-health.json"
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert p["status"] == "ok" and p["version"]' \
  "${work_dir}/oracle-health.json"

echo "[3/7] TNS alias catalog"
curl -fsS "${FOCUSLOADER_API_URL}/api/v1/tns/aliases" -o "${work_dir}/aliases.json"
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert p["aliases"] and p["firstAlias"] == p["aliases"][0]' \
  "${work_dir}/aliases.json"

echo "[4/7] Fixed schema deployment prerequisites"
test -x "${SQL_SCRIPTS_DIR}/deploy_focus_schema_with_sqlloader_audit.sh"
test -x "${SQL_SCRIPTS_DIR}/run_deploy_focus_schema_with_sqlloader_audit.sh"
test -r "${SQL_SCRIPTS_DIR}/install_finops_oml.sql"
test -r "${SQL_SCRIPTS_DIR}/uninstall_finops_oml.sql"
test -w "${SQL_SCRIPTS_DIR}/focus.conf"
test -w "$(dirname "${SQL_SCRIPTS_DIR}")/focus.conf"
command -v sqlplus >/dev/null

echo "[5/7] Loader execution APIs"
status=$(curl -sS -o "${work_dir}/loader-jobs.json" -w '%{http_code}' \
  "${FOCUSLOADER_API_URL}/api/v1/loader/jobs")
[[ "${status}" == "200" ]]
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert isinstance(p["activeJobId"], str) and isinstance(p["jobs"], list)' \
  "${work_dir}/loader-jobs.json"
status=$(curl -sS -o "${work_dir}/oracle-loader-jobs.json" -w '%{http_code}' \
  "${ORACLE_API_URL}/api/v1/loader/jobs")
[[ "${status}" == "200" ]]
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert isinstance(p["activeJobId"], str) and isinstance(p["jobs"], list)' \
  "${work_dir}/oracle-loader-jobs.json"

echo "[6/7] Streamlit health"
health=$(curl -fsS "${UI_URL}/_stcore/health")
[[ "${health}" == "ok" ]]

echo "[7/7] Streamlit page"
curl -fsS "${UI_URL}/" -o "${work_dir}/streamlit.html"
grep -qi 'streamlit' "${work_dir}/streamlit.html"

echo "All Streamlit frontend checks passed."
