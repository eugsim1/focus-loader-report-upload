#!/usr/bin/env bash
set -Eeuo pipefail

API_URL=${FOCUS_API_URL:-http://127.0.0.1:8080}
UI_URL=${FOCUS_UI_URL:-http://127.0.0.1:8501}
PYTHON_BIN=${PYTHON_BIN:-python3}
SQL_SCRIPTS_DIR=${FOCUS_SQL_SCRIPTS_DIR:-/opt/focus-loader/sql_scripts}

work_dir=$(mktemp -d)
cleanup() {
  rm -rf -- "${work_dir}"
}
trap cleanup EXIT

echo "[1/5] Go backend health"
curl -fsS "${API_URL}/api/v1/health" -o "${work_dir}/health.json"
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert p["status"] == "ok" and p["version"]' \
  "${work_dir}/health.json"

echo "[2/5] TNS alias catalog"
curl -fsS "${API_URL}/api/v1/tns/aliases" -o "${work_dir}/aliases.json"
"${PYTHON_BIN}" -c \
  'import json,sys; p=json.load(open(sys.argv[1], encoding="utf-8")); assert p["aliases"] and p["firstAlias"] == p["aliases"][0]' \
  "${work_dir}/aliases.json"

echo "[3/5] Fixed schema deployment prerequisites"
test -x "${SQL_SCRIPTS_DIR}/deploy_focus_schema_with_sqlloader_audit.sh"
test -w "${SQL_SCRIPTS_DIR}/focus.conf"
test -w "$(dirname "${SQL_SCRIPTS_DIR}")/focus.conf"
command -v sqlplus >/dev/null

echo "[4/5] Streamlit health"
health=$(curl -fsS "${UI_URL}/_stcore/health")
[[ "${health}" == "ok" ]]

echo "[5/5] Streamlit page"
curl -fsS "${UI_URL}/" -o "${work_dir}/streamlit.html"
grep -qi 'streamlit' "${work_dir}/streamlit.html"

echo "All Streamlit frontend checks passed."
