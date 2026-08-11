#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# -ne 3 ]]; then
  echo "Usage: $0 INPUT_CONFIG OUTPUT_CONFIG ABSOLUTE_OCI_DIRECTORY" >&2
  exit 64
fi

input_config=$1
output_config=$2
oci_directory=${3%/}

if [[ ! -f "${input_config}" || "${oci_directory}" != /* ]]; then
  echo "ERROR: input must be a regular file and OCI directory must be absolute" >&2
  exit 64
fi

awk -v target="${oci_directory}" '
  /^[[:space:]]*(key_file|security_token_file)[[:space:]]*=/ {
    separator = index($0, "=")
    name = substr($0, 1, separator - 1)
    value = substr($0, separator + 1)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", name)
    gsub(/^[[:space:]"]+|[[:space:]"]+$/, "", value)
    normalized = value
    gsub(/\\/, "/", normalized)
    marker = index(normalized, ".oci/")
    if (marker > 0) {
      relative = substr(normalized, marker + 5)
    } else {
      count = split(normalized, components, "/")
      relative = components[count]
    }
    if (relative == "" || relative ~ /(^|\/)\.\.($|\/)/) {
      print "ERROR: empty file name for " name > "/dev/stderr"
      exit 65
    }
    print name "=" target "/" relative
    next
  }
  { print }
' "${input_config}" > "${output_config}"
