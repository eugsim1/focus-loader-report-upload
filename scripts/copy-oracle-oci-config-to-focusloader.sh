#!/usr/bin/env bash
set -Eeuo pipefail

if [[ ${EUID} -ne 0 ]]; then
  echo "ERROR: run this script with sudo or as root" >&2
  exit 1
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
SOURCE_USER=${SOURCE_USER:-oracle}
TARGET_USER=${TARGET_USER:-focusloader}

for account in "${SOURCE_USER}" "${TARGET_USER}"; do
  if ! getent passwd "${account}" >/dev/null; then
    echo "ERROR: Linux account ${account} does not exist" >&2
    exit 1
  fi
done

source_home=$(getent passwd "${SOURCE_USER}" | cut -d: -f6)
target_home=$(getent passwd "${TARGET_USER}" | cut -d: -f6)
target_group=$(id -gn "${TARGET_USER}")
source_dir=${source_home}/.oci
target_dir=${target_home}/.oci

if [[ ! -f "${source_dir}/config" ]]; then
  echo "ERROR: ${source_dir}/config does not exist" >&2
  exit 1
fi
if [[ -L "${source_dir}" || -L "${target_dir}" ]]; then
  echo "ERROR: source and target .oci directories must not be symbolic links" >&2
  exit 1
fi
if find "${source_dir}" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
  echo "ERROR: ${source_dir} contains a symlink or special file; refusing unsafe copy" >&2
  exit 1
fi
if [[ -e "${target_dir}" ]] \
  && find "${target_dir}" -mindepth 1 ! -type d ! -type f -print -quit | grep -q .; then
  echo "ERROR: ${target_dir} contains a symlink or special file; refusing unsafe overwrite" >&2
  exit 1
fi

install -d -o "${TARGET_USER}" -g "${target_group}" -m 0700 "${target_dir}"
while IFS= read -r -d '' source_path; do
  relative_path=${source_path#"${source_dir}/"}
  install -d -o "${TARGET_USER}" -g "${target_group}" -m 0700 \
    "${target_dir}/${relative_path}"
done < <(find "${source_dir}" -mindepth 1 -type d -print0)

while IFS= read -r -d '' source_path; do
  relative_path=${source_path#"${source_dir}/"}
  install -o "${TARGET_USER}" -g "${target_group}" -m 0600 -- \
    "${source_path}" "${target_dir}/${relative_path}"
done < <(find "${source_dir}" -mindepth 1 -type f -print0)

temporary_config=$(mktemp "${target_dir}/.config.XXXXXX")
trap 'rm -f -- "${temporary_config}"' EXIT
"${SCRIPT_DIR}/rewrite-oci-config-paths.sh" \
  "${target_dir}/config" "${temporary_config}" "${target_dir}"
while IFS= read -r referenced_file; do
  if [[ ! -f "${referenced_file}" ]]; then
    echo "ERROR: rewritten OCI config references a file that was not copied: ${referenced_file}" >&2
    exit 1
  fi
done < <(awk -F= '/^[[:space:]]*(key_file|security_token_file)[[:space:]]*=/ {sub(/^[^=]*=/, ""); print}' "${temporary_config}")
chown "${TARGET_USER}:${target_group}" "${temporary_config}"
chmod 0600 "${temporary_config}"
mv -f -- "${temporary_config}" "${target_dir}/config"
trap - EXIT

find "${target_dir}" -type d -exec chown "${TARGET_USER}:${target_group}" {} +
find "${target_dir}" -type d -exec chmod 0700 {} +
find "${target_dir}" -type f -exec chown "${TARGET_USER}:${target_group}" {} +
find "${target_dir}" -type f -exec chmod 0600 {} +

if command -v restorecon >/dev/null 2>&1; then
  restorecon -RF "${target_dir}" >/dev/null 2>&1 || true
fi

echo "Copied ${source_dir} to ${target_dir}."
echo "Rewrote key_file and security_token_file entries to absolute paths under ${target_dir}."
