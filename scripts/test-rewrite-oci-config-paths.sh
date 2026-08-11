#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
work_dir=$(mktemp -d)
trap 'rm -rf -- "${work_dir}"' EXIT

printf '%s\n' \
  '[DEFAULT]' \
  'user=ocid1.user.oc1..test' \
  'key_file=/home/oracle/.oci/api_key.pem' \
  'security_token_file = ~/.oci/sessions/DEFAULT/token' \
  > "${work_dir}/input"

"${SCRIPT_DIR}/rewrite-oci-config-paths.sh" \
  "${work_dir}/input" "${work_dir}/output" "/home/focusloader/.oci"

grep -Fx 'key_file=/home/focusloader/.oci/api_key.pem' "${work_dir}/output"
grep -Fx 'security_token_file=/home/focusloader/.oci/sessions/DEFAULT/token' "${work_dir}/output"
grep -Fx 'user=ocid1.user.oc1..test' "${work_dir}/output"

echo "OCI config path rewrite test passed."
