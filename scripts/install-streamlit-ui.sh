#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "ERROR: run this installer with sudo or as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
SOURCE_DIR=$(cd -- "${SCRIPT_DIR}/.." && pwd)
APP_DIR=${APP_DIR:-/opt/focus-loader}
SERVICE_USER=${SERVICE_USER:-focusloader}
SERVICE_GROUP=${SERVICE_GROUP:-focusloader}
PYTHON_BIN=${PYTHON_BIN:-python3.11}
TNS_ADMIN=${TNS_ADMIN:-/opt/oracle/wallet}
FOCUS_API_URL=${FOCUS_API_URL:-http://127.0.0.1:8080}
SQLPLUS_BIN=${SQLPLUS_BIN:-$(command -v sqlplus || true)}

if ! id "${SERVICE_USER}" >/dev/null 2>&1; then
  echo "ERROR: service account ${SERVICE_USER} does not exist" >&2
  echo "Create it first: sudo useradd --system --create-home --shell /bin/bash ${SERVICE_USER}" >&2
  exit 1
fi
if ! command -v "${PYTHON_BIN}" >/dev/null 2>&1; then
  echo "ERROR: ${PYTHON_BIN} is not installed" >&2
  echo "Install Python 3.11 and its pip/venv support, then rerun this script." >&2
  exit 1
fi
if [[ ! -x "${APP_DIR}/focus-loader-report-upload" ]]; then
  echo "ERROR: ${APP_DIR}/focus-loader-report-upload is not installed or executable" >&2
  exit 1
fi
if ! runuser -u "${SERVICE_USER}" -- test -x "${APP_DIR}/focus-loader-report-upload"; then
  echo "ERROR: ${SERVICE_USER} cannot execute ${APP_DIR}/focus-loader-report-upload" >&2
  exit 1
fi
if [[ ! -r "${TNS_ADMIN}/tnsnames.ora" ]]; then
  echo "ERROR: cannot read ${TNS_ADMIN}/tnsnames.ora" >&2
  exit 1
fi
if ! runuser -u "${SERVICE_USER}" -- test -r "${TNS_ADMIN}/tnsnames.ora"; then
  echo "ERROR: ${SERVICE_USER} cannot read ${TNS_ADMIN}/tnsnames.ora" >&2
  exit 1
fi
if [[ -z "${SQLPLUS_BIN}" || ! -x "${SQLPLUS_BIN}" ]]; then
  echo "ERROR: sqlplus is required for schema deployment but was not found" >&2
  echo "Set SQLPLUS_BIN=/absolute/path/to/sqlplus and rerun this installer." >&2
  exit 1
fi
if ! runuser -u "${SERVICE_USER}" -- test -x "${SQLPLUS_BIN}"; then
  echo "ERROR: ${SERVICE_USER} cannot execute ${SQLPLUS_BIN}" >&2
  exit 1
fi

install -d -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0750 "${APP_DIR}/streamlit-ui"
install -d -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0750 "${APP_DIR}/streamlit-ui/.streamlit"
install -d -o root -g "${SERVICE_GROUP}" -m 0750 "${APP_DIR}/sql_scripts"
install -d -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0750 "${APP_DIR}/work_report_dir"
install -d -o root -g "${SERVICE_GROUP}" -m 0750 /etc/focus-loader

install -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0640 \
  "${SOURCE_DIR}/streamlit-ui/app.py" \
  "${SOURCE_DIR}/streamlit-ui/focus_api.py" \
  "${SOURCE_DIR}/streamlit-ui/command_builder.py" \
  "${SOURCE_DIR}/streamlit-ui/requirements.txt" \
  "${APP_DIR}/streamlit-ui/"
install -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0640 \
  "${SOURCE_DIR}/streamlit-ui/.streamlit/config.toml" \
  "${APP_DIR}/streamlit-ui/.streamlit/config.toml"
install -o root -g "${SERVICE_GROUP}" -m 0750 \
  "${SOURCE_DIR}/sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh" \
  "${APP_DIR}/sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh"
if [[ ! -e "${APP_DIR}/sql_scripts/focus.conf" ]]; then
  install -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0640 \
    "${SOURCE_DIR}/sql_scripts/focus.conf" \
    "${APP_DIR}/sql_scripts/focus.conf"
else
  chown "${SERVICE_USER}:${SERVICE_GROUP}" "${APP_DIR}/sql_scripts/focus.conf"
  chmod 0640 "${APP_DIR}/sql_scripts/focus.conf"
fi
if [[ ! -e "${APP_DIR}/focus.conf" ]]; then
  install -o "${SERVICE_USER}" -g "${SERVICE_GROUP}" -m 0640 \
    "${SOURCE_DIR}/sql_scripts/focus.conf" "${APP_DIR}/focus.conf"
fi
if ! runuser -u "${SERVICE_USER}" -- test -w "${APP_DIR}/focus.conf"; then
  echo "ERROR: ${SERVICE_USER} must be able to update ${APP_DIR}/focus.conf" >&2
  echo "Review its ownership and mode, then rerun this installer." >&2
  exit 1
fi
if ! runuser -u "${SERVICE_USER}" -- test -w "${APP_DIR}/sql_scripts/focus.conf"; then
  echo "ERROR: ${SERVICE_USER} must be able to update ${APP_DIR}/sql_scripts/focus.conf" >&2
  exit 1
fi

"${PYTHON_BIN}" -m venv "${APP_DIR}/streamlit-ui/.venv"
"${APP_DIR}/streamlit-ui/.venv/bin/python" -m pip install --no-cache-dir --upgrade pip
"${APP_DIR}/streamlit-ui/.venv/bin/python" -m pip install --no-cache-dir \
  -r "${APP_DIR}/streamlit-ui/requirements.txt"
chown -R "${SERVICE_USER}:${SERVICE_GROUP}" "${APP_DIR}/streamlit-ui/.venv"

SQLPLUS_DIR=$(cd -- "$(dirname -- "${SQLPLUS_BIN}")" && pwd)
printf 'TNS_ADMIN=%s\nFOCUS_SQL_SCRIPTS_DIR=%s\nFOCUS_LOADER_EXECUTABLE=%s\nFOCUS_LOADER_WORK_DIR=%s\nPATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin\n' \
  "${TNS_ADMIN}" "${APP_DIR}/sql_scripts" \
  "${APP_DIR}/focus-loader-report-upload" "${APP_DIR}" "${SQLPLUS_DIR}" \
  > /etc/focus-loader/tns-gui.env
chown root:"${SERVICE_GROUP}" /etc/focus-loader/tns-gui.env
chmod 0640 /etc/focus-loader/tns-gui.env

printf 'FOCUS_API_URL=%s\nFOCUS_LOADER_EXECUTABLE=%s\n' \
  "${FOCUS_API_URL}" "${APP_DIR}/focus-loader-report-upload" \
  > /etc/focus-loader/streamlit.env
chown root:"${SERVICE_GROUP}" /etc/focus-loader/streamlit.env
chmod 0640 /etc/focus-loader/streamlit.env

install -o root -g root -m 0644 \
  "${SOURCE_DIR}/deploy/focus-loader-tns-gui.service.example" \
  /etc/systemd/system/focus-loader-tns-gui.service
install -o root -g root -m 0644 \
  "${SOURCE_DIR}/deploy/focus-loader-streamlit.service.example" \
  /etc/systemd/system/focus-loader-streamlit.service

systemctl daemon-reload
systemctl enable focus-loader-tns-gui.service
systemctl enable focus-loader-streamlit.service
systemctl restart focus-loader-tns-gui.service
systemctl restart focus-loader-streamlit.service

echo "Waiting for local health endpoints..."
for _ in {1..30}; do
  if curl -fsS "${FOCUS_API_URL}/api/v1/health" >/dev/null \
    && curl -fsS http://127.0.0.1:8501/_stcore/health >/dev/null; then
    echo "Streamlit UI installed successfully."
    echo "Use an SSH tunnel and open http://127.0.0.1:8501/ from your workstation."
    exit 0
  fi
  sleep 2
done

echo "ERROR: services did not become healthy within 60 seconds" >&2
systemctl --no-pager --full status focus-loader-tns-gui.service || true
systemctl --no-pager --full status focus-loader-streamlit.service || true
exit 1
