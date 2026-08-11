#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "ERROR: run this installer with sudo or as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
SOURCE_DIR=$(cd -- "${SCRIPT_DIR}/.." && pwd)
APP_DIR=/opt/focus-loader
FOCUSLOADER_USER=focusloader
ORACLE_USER=oracle
PYTHON_BIN=${PYTHON_BIN:-python3.11}
TNS_ADMIN=${TNS_ADMIN:-/opt/oracle/wallet}
FOCUSLOADER_API_URL=http://127.0.0.1:8080
ORACLE_API_URL=http://127.0.0.1:8081
ORACLE_SOURCE_DIR=/home/oracle/focus-loader-report-upload
ORACLE_EXECUTABLE=${ORACLE_SOURCE_DIR}/dist/focus-loader-report-upload-linux-amd64
ORACLE_SQL_SCRIPTS_DIR=${ORACLE_SOURCE_DIR}/sql_scripts
SQLPLUS_BIN=${SQLPLUS_BIN:-$(command -v sqlplus || true)}

for account in "${FOCUSLOADER_USER}" "${ORACLE_USER}"; do
  if ! id "${account}" >/dev/null 2>&1; then
    echo "ERROR: service account ${account} does not exist" >&2
    exit 1
  fi
done
FOCUSLOADER_GROUP=$(id -gn "${FOCUSLOADER_USER}")
ORACLE_GROUP=$(id -gn "${ORACLE_USER}")
if ! command -v "${PYTHON_BIN}" >/dev/null 2>&1; then
  echo "ERROR: ${PYTHON_BIN} is not installed" >&2
  exit 1
fi
if [[ -z "${SQLPLUS_BIN}" || ! -x "${SQLPLUS_BIN}" ]]; then
  echo "ERROR: sqlplus is required; set SQLPLUS_BIN to its absolute path" >&2
  exit 1
fi
if [[ ! -r "${TNS_ADMIN}/tnsnames.ora" ]]; then
  echo "ERROR: cannot read ${TNS_ADMIN}/tnsnames.ora" >&2
  exit 1
fi

install -d -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0750 "${APP_DIR}"
if [[ ! -x "${APP_DIR}/focus-loader-report-upload" ]]; then
  build_binary=${SOURCE_DIR}/dist/focus-loader-report-upload-linux-amd64
  if [[ ! -f "${build_binary}" ]]; then
    echo "ERROR: ${APP_DIR}/focus-loader-report-upload is missing and ${build_binary} is unavailable" >&2
    echo "Build the Linux binary first, then rerun this installer." >&2
    exit 1
  fi
  install -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0750 \
    "${build_binary}" "${APP_DIR}/focus-loader-report-upload"
fi

for check in \
  "${FOCUSLOADER_USER}:${APP_DIR}/focus-loader-report-upload" \
  "${ORACLE_USER}:${ORACLE_EXECUTABLE}"; do
  account=${check%%:*}
  executable=${check#*:}
  if [[ "${executable}" != /* || ! -x "${executable}" ]] \
    || ! runuser -u "${account}" -- test -x "${executable}"; then
    echo "ERROR: ${account} cannot execute ${executable}" >&2
    exit 1
  fi
done
for account in "${FOCUSLOADER_USER}" "${ORACLE_USER}"; do
  if ! runuser -u "${account}" -- test -r "${TNS_ADMIN}/tnsnames.ora"; then
    echo "ERROR: ${account} cannot read ${TNS_ADMIN}/tnsnames.ora" >&2
    exit 1
  fi
  if ! runuser -u "${account}" -- test -x "${SQLPLUS_BIN}"; then
    echo "ERROR: ${account} cannot execute ${SQLPLUS_BIN}" >&2
    exit 1
  fi
done
if [[ ! -x "${ORACLE_SQL_SCRIPTS_DIR}/deploy_focus_schema_with_sqlloader_audit.sh" ]]; then
  echo "ERROR: Oracle deployment script is missing from ${ORACLE_SQL_SCRIPTS_DIR}" >&2
  exit 1
fi
for config in "${ORACLE_SOURCE_DIR}/focus.conf" "${ORACLE_SQL_SCRIPTS_DIR}/focus.conf"; do
  if ! runuser -u "${ORACLE_USER}" -- test -w "${config}"; then
    echo "ERROR: ${ORACLE_USER} must be able to update ${config}" >&2
    exit 1
  fi
done

echo "Copying Oracle OCI CLI configuration to ${FOCUSLOADER_USER}..."
SOURCE_USER="${ORACLE_USER}" TARGET_USER="${FOCUSLOADER_USER}" \
  bash "${SCRIPT_DIR}/copy-oracle-oci-config-to-focusloader.sh"

install -d -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0750 "${APP_DIR}/streamlit-ui/.streamlit"
install -d -o root -g "${FOCUSLOADER_GROUP}" -m 0750 "${APP_DIR}/sql_scripts"
install -d -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0750 "${APP_DIR}/work_report_dir"
install -d -o "${ORACLE_USER}" -g "${ORACLE_GROUP}" -m 0750 "${ORACLE_SOURCE_DIR}/work_report_dir"
install -d -o root -g "${FOCUSLOADER_GROUP}" -m 0750 /etc/focus-loader

install -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0640 \
  "${SOURCE_DIR}/streamlit-ui/app.py" \
  "${SOURCE_DIR}/streamlit-ui/focus_api.py" \
  "${SOURCE_DIR}/streamlit-ui/command_builder.py" \
  "${SOURCE_DIR}/streamlit-ui/requirements.txt" \
  "${APP_DIR}/streamlit-ui/"
install -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0640 \
  "${SOURCE_DIR}/streamlit-ui/.streamlit/config.toml" \
  "${APP_DIR}/streamlit-ui/.streamlit/config.toml"
install -o root -g "${FOCUSLOADER_GROUP}" -m 0750 \
  "${SOURCE_DIR}/sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh" \
  "${APP_DIR}/sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh"
if [[ ! -e "${APP_DIR}/sql_scripts/focus.conf" ]]; then
  install -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0640 \
    "${SOURCE_DIR}/sql_scripts/focus.conf" "${APP_DIR}/sql_scripts/focus.conf"
else
  chown "${FOCUSLOADER_USER}:${FOCUSLOADER_GROUP}" "${APP_DIR}/sql_scripts/focus.conf"
  chmod 0640 "${APP_DIR}/sql_scripts/focus.conf"
fi
if [[ ! -e "${APP_DIR}/focus.conf" ]]; then
  install -o "${FOCUSLOADER_USER}" -g "${FOCUSLOADER_GROUP}" -m 0640 \
    "${SOURCE_DIR}/sql_scripts/focus.conf" "${APP_DIR}/focus.conf"
fi
for config in "${APP_DIR}/focus.conf" "${APP_DIR}/sql_scripts/focus.conf"; do
  if ! runuser -u "${FOCUSLOADER_USER}" -- test -w "${config}"; then
    echo "ERROR: ${FOCUSLOADER_USER} must be able to update ${config}" >&2
    exit 1
  fi
done

"${PYTHON_BIN}" -m venv "${APP_DIR}/streamlit-ui/.venv"
"${APP_DIR}/streamlit-ui/.venv/bin/python" -m pip install --no-cache-dir --upgrade pip
"${APP_DIR}/streamlit-ui/.venv/bin/python" -m pip install --no-cache-dir \
  -r "${APP_DIR}/streamlit-ui/requirements.txt"
chown -R "${FOCUSLOADER_USER}:${FOCUSLOADER_GROUP}" "${APP_DIR}/streamlit-ui/.venv"

SQLPLUS_DIR=$(cd -- "$(dirname -- "${SQLPLUS_BIN}")" && pwd)
printf 'HOME=%s\nTNS_ADMIN=%s\nFOCUS_SQL_SCRIPTS_DIR=%s\nFOCUS_LOADER_EXECUTABLE=%s\nFOCUS_LOADER_WORK_DIR=%s\nPATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin\n' \
  "$(getent passwd "${FOCUSLOADER_USER}" | cut -d: -f6)" "${TNS_ADMIN}" \
  "${APP_DIR}/sql_scripts" "${APP_DIR}/focus-loader-report-upload" "${APP_DIR}" "${SQLPLUS_DIR}" \
  > /etc/focus-loader/tns-gui.env
printf 'HOME=%s\nTNS_ADMIN=%s\nFOCUS_SQL_SCRIPTS_DIR=%s\nFOCUS_LOADER_EXECUTABLE=%s\nFOCUS_LOADER_WORK_DIR=%s\nPATH=%s:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin\n' \
  "$(getent passwd "${ORACLE_USER}" | cut -d: -f6)" "${TNS_ADMIN}" \
  "${ORACLE_SQL_SCRIPTS_DIR}" "${ORACLE_EXECUTABLE}" "${ORACLE_SOURCE_DIR}" "${SQLPLUS_DIR}" \
  > /etc/focus-loader/tns-gui-oracle.env
chown root:"${FOCUSLOADER_GROUP}" /etc/focus-loader/tns-gui.env /etc/focus-loader/tns-gui-oracle.env
chmod 0640 /etc/focus-loader/tns-gui.env /etc/focus-loader/tns-gui-oracle.env

printf 'FOCUS_API_URL=%s\nFOCUS_API_URL_FOCUSLOADER=%s\nFOCUS_API_URL_ORACLE=%s\nFOCUS_LOADER_EXECUTABLE=%s\nFOCUS_LOADER_EXECUTABLE_FOCUSLOADER=%s\nFOCUS_LOADER_EXECUTABLE_ORACLE=%s\n' \
  "${FOCUSLOADER_API_URL}" "${FOCUSLOADER_API_URL}" "${ORACLE_API_URL}" \
  "${APP_DIR}/focus-loader-report-upload" "${APP_DIR}/focus-loader-report-upload" "${ORACLE_EXECUTABLE}" \
  > /etc/focus-loader/streamlit.env
chown root:"${FOCUSLOADER_GROUP}" /etc/focus-loader/streamlit.env
chmod 0640 /etc/focus-loader/streamlit.env

install -o root -g root -m 0644 "${SOURCE_DIR}/deploy/focus-loader-tns-gui.service.example" /etc/systemd/system/focus-loader-tns-gui.service
install -o root -g root -m 0644 "${SOURCE_DIR}/deploy/focus-loader-tns-gui-oracle.service.example" /etc/systemd/system/focus-loader-tns-gui-oracle.service
install -o root -g root -m 0644 "${SOURCE_DIR}/deploy/focus-loader-streamlit.service.example" /etc/systemd/system/focus-loader-streamlit.service

systemctl daemon-reload
systemctl enable focus-loader-tns-gui.service focus-loader-tns-gui-oracle.service focus-loader-streamlit.service
systemctl restart focus-loader-tns-gui.service focus-loader-tns-gui-oracle.service
systemctl restart focus-loader-streamlit.service

echo "Waiting for both local backends and Streamlit..."
for iteration in {1..30}; do
  echo "Health iteration ${iteration}/30"
  if curl -fsS "${FOCUSLOADER_API_URL}/api/v1/health" >/dev/null \
    && curl -fsS "${ORACLE_API_URL}/api/v1/health" >/dev/null \
    && curl -fsS http://127.0.0.1:8501/_stcore/health >/dev/null; then
    echo "Streamlit UI installed successfully with focusloader and oracle backends."
    echo "Use an SSH tunnel and open http://127.0.0.1:8501/ from your workstation."
    exit 0
  fi
  sleep 2
done

echo "ERROR: services did not become healthy within 60 seconds" >&2
systemctl --no-pager --full status focus-loader-tns-gui.service || true
systemctl --no-pager --full status focus-loader-tns-gui-oracle.service || true
systemctl --no-pager --full status focus-loader-streamlit.service || true
exit 1
