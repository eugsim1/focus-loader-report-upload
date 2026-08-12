#!/usr/bin/env bash

# Shared, source-only helpers for the Linux OCI Bastion tunnel launchers.

declare -Ag FOCUS_SETTINGS=()
declare -Ag FOCUS_ALLOWED_SETTINGS=()
declare -ag FOCUS_OCI_GLOBAL_ARGUMENTS=()
FOCUS_BASTION_SESSION_ID=""
FOCUS_BASTION_SESSION_CREATED=0

focus_die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

focus_load_parameter_file() {
  local file=$1 line key value line_number=0
  [[ -f "$file" ]] || focus_die "Parameter file is not a file: $file"
  FOCUS_PARAMETER_FILE=$(cd -- "$(dirname -- "$file")" && pwd)/$(basename -- "$file")
  FOCUS_PARAMETER_DIRECTORY=$(dirname -- "$FOCUS_PARAMETER_FILE")
  while IFS= read -r line || [[ -n "$line" ]]; do
    ((line_number += 1))
    line=${line%$'\r'}
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue
    [[ "$line" =~ ^[[:space:]]*[#\;] ]] && continue
    [[ "$line" == *"="* ]] || focus_die "Parameter file line $line_number must use Name=Value format."
    key=${line%%=*}; value=${line#*=}
    key=$(printf '%s' "$key" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    value=$(printf '%s' "$value" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    if [[ "$value" == \"*\" && "$value" == *\" ]] || [[ "$value" == \'*\' && "$value" == *\' ]]; then
      value=${value:1:${#value}-2}
    fi
    [[ -n ${FOCUS_ALLOWED_SETTINGS[$key]+x} ]] || focus_die "Unsupported parameter-file key '$key' on line $line_number."
    [[ -z ${FOCUS_SETTINGS[$key]+x} ]] || focus_die "Duplicate parameter-file key '$key'."
    FOCUS_SETTINGS[$key]=$value
  done < "$FOCUS_PARAMETER_FILE"
  if [[ -n ${FOCUS_SETTINGS[AssetsVersion]:-} && ${FOCUS_SETTINGS[AssetsVersion]} != 1 ]]; then
    focus_die "Unsupported AssetsVersion '${FOCUS_SETTINGS[AssetsVersion]}'."
  fi
  printf 'Loaded parameters from: %s\n' "$FOCUS_PARAMETER_FILE"
}

focus_setting() {
  local name=$1 default_value=${2-}
  if [[ -n ${FOCUS_SETTINGS[$name]+x} ]]; then
    printf '%s' "${FOCUS_SETTINGS[$name]}"
  else
    printf '%s' "$default_value"
  fi
}

focus_boolean_setting() {
  local name=$1 default_value=$2 value
  value=$(focus_setting "$name" "$default_value")
  case ${value,,} in
    true|1|yes) printf 'true' ;;
    false|0|no) printf 'false' ;;
    *) focus_die "Parameter '$name' must be true or false." ;;
  esac
}

focus_integer_setting() {
  local name=$1 default_value=$2 minimum=$3 maximum=$4 value
  value=$(focus_setting "$name" "$default_value")
  [[ "$value" =~ ^[0-9]+$ ]] || focus_die "Parameter '$name' must be an integer."
  (( value >= minimum && value <= maximum )) || focus_die "Parameter '$name' must be from $minimum through $maximum."
  printf '%s' "$value"
}

focus_expand_path() {
  local value=$1 base_directory=$2
  case "$value" in
    '~') value=$HOME ;;
    '~/'*) value=$HOME/${value#'~/'} ;;
    '$HOME') value=$HOME ;;
    '$HOME/'*) value=$HOME/${value#'$HOME/'} ;;
    '${HOME}') value=$HOME ;;
    '${HOME}/'*) value=$HOME/${value#'${HOME}/'} ;;
  esac
  if [[ "$value" != /* ]]; then
    value=$base_directory/$value
  fi
  printf '%s' "$value"
}

focus_require_value() {
  local name=$1 value=$2
  [[ -n "$value" ]] || focus_die "Missing required parameter '$name'."
}

focus_validate_ipv4() {
  local value=$1 octet
  [[ "$value" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || focus_die "PrivateIp must be an IPv4 address: $value"
  IFS=. read -r -a octets <<< "$value"
  for octet in "${octets[@]}"; do (( octet <= 255 )) || focus_die "PrivateIp must be an IPv4 address: $value"; done
}

focus_command_path() {
  command -v -- "$1" 2>/dev/null || focus_die "Required executable is not available: $1"
}

focus_local_port_in_use() {
  local port=$1
  if command -v ss >/dev/null 2>&1; then
    ss -H -ltn "sport = :$port" 2>/dev/null | grep -q .
  elif command -v netstat >/dev/null 2>&1; then
    netstat -ltn 2>/dev/null | awk -v suffix=":$port" '$4 ~ suffix "$" { found=1 } END { exit !found }'
  else
    return 1
  fi
}

focus_oci_json() {
  "$FOCUS_OCI_EXECUTABLE" "$@" "${FOCUS_OCI_GLOBAL_ARGUMENTS[@]}" --output json
}

focus_cleanup_bastion_session() {
  if (( FOCUS_BASTION_SESSION_CREATED == 1 )) && [[ -n "$FOCUS_BASTION_SESSION_ID" ]] && [[ "$FOCUS_KEEP_SESSION" != true ]]; then
    printf 'Deleting temporary Bastion session %s...\n' "$FOCUS_BASTION_SESSION_ID"
    FOCUS_BASTION_SESSION_CREATED=0
    "$FOCUS_OCI_EXECUTABLE" bastion session delete --session-id "$FOCUS_BASTION_SESSION_ID" --force \
      "${FOCUS_OCI_GLOBAL_ARGUMENTS[@]}" >/dev/null 2>&1 || \
      printf 'WARNING: could not delete Bastion session %s automatically.\n' "$FOCUS_BASTION_SESSION_ID" >&2
  elif [[ -n "$FOCUS_BASTION_SESSION_ID" && "$FOCUS_KEEP_SESSION" == true ]]; then
    printf 'Keeping Bastion session until its TTL expires: %s\n' "$FOCUS_BASTION_SESSION_ID"
  fi
}

focus_open_bastion_tunnels() {
  local auth_mode=$1 profile=$2 config_file=$3
  local bastion_state session_display_name session_state session_json
  local iteration elapsed=0 forward_spec public_key private_key
  local -a ssh_arguments=(-N) forward_specs=()
  local -A local_port_targets=()
  local -a local_ports=() remote_ports=()

  private_key=$(focus_expand_path "$FOCUS_SSH_PRIVATE_KEY_PATH" "$FOCUS_PARAMETER_DIRECTORY")
  public_key=${FOCUS_SSH_PUBLIC_KEY_PATH:-${private_key}.pub}
  public_key=$(focus_expand_path "$public_key" "$FOCUS_PARAMETER_DIRECTORY")
  [[ -f "$private_key" ]] || focus_die "SshPrivateKeyPath is not a file: $private_key"
  [[ -f "$public_key" ]] || focus_die "SshPublicKeyPath is not a file: $public_key"

  focus_add_mapping() {
    local local_port=$1 remote_port=$2
    if [[ -n ${local_port_targets[$local_port]+x} ]]; then
      [[ ${local_port_targets[$local_port]} == "$remote_port" ]] && return
      focus_die "Local TCP port $local_port is assigned to more than one remote port."
    fi
    local_port_targets[$local_port]=$remote_port
    local_ports+=("$local_port"); remote_ports+=("$remote_port")
  }

  focus_add_mapping "$FOCUS_SSH_LOCAL_PORT" 22
  focus_add_mapping 8501 8501
  focus_add_mapping 8502 8502
  focus_add_mapping 5901 5901
  [[ "$FOCUS_OPTIONAL_PORT1" == 0 ]] || focus_add_mapping "$FOCUS_OPTIONAL_PORT1" "$FOCUS_OPTIONAL_PORT1"
  [[ "$FOCUS_OPTIONAL_PORT2" == 0 ]] || focus_add_mapping "$FOCUS_OPTIONAL_PORT2" "$FOCUS_OPTIONAL_PORT2"
  if [[ "$FOCUS_LOCAL_PORT" == 0 || "$FOCUS_REMOTE_PORT" == 0 ]]; then
    [[ "$FOCUS_LOCAL_PORT" == 0 && "$FOCUS_REMOTE_PORT" == 0 ]] || focus_die 'LocalPort and RemotePort must both be zero or both be supplied.'
  else
    focus_add_mapping "$FOCUS_LOCAL_PORT" "$FOCUS_REMOTE_PORT"
  fi

  for iteration in "${!local_ports[@]}"; do
    if focus_local_port_in_use "${local_ports[$iteration]}"; then
      if [[ "$FOCUS_DRY_RUN" == true ]]; then
        printf 'WARNING: local TCP port %s is already in use.\n' "${local_ports[$iteration]}" >&2
      else
        focus_die "Local TCP port ${local_ports[$iteration]} is already in use."
      fi
    fi
  done
  if (( FOCUS_SSH_LOCAL_PORT < 1024 && EUID != 0 )); then
    if [[ "$FOCUS_DRY_RUN" == true ]]; then
      printf 'WARNING: SshLocalPort=%s is privileged on Linux; use 2222 for an unprivileged bind.\n' "$FOCUS_SSH_LOCAL_PORT" >&2
    else
      focus_die "SshLocalPort=$FOCUS_SSH_LOCAL_PORT is privileged on Linux. Set SshLocalPort=2222 in the parameter file or run under an approved privilege model."
    fi
  fi

  printf 'Loopback-only tunnels:\n'
  for iteration in "${!local_ports[@]}"; do
    printf '  127.0.0.1:%s -> 127.0.0.1:%s\n' "${local_ports[$iteration]}" "${remote_ports[$iteration]}"
  done
  if [[ "$FOCUS_DRY_RUN" == true ]]; then
    printf 'DRY RUN: no OCI request, Bastion session, or SSH process was created.\n'
    return
  fi

  FOCUS_OCI_EXECUTABLE=$(focus_command_path "$FOCUS_OCI_EXECUTABLE")
  FOCUS_SSH_EXECUTABLE=$(focus_command_path "$FOCUS_SSH_EXECUTABLE")
  FOCUS_OCI_GLOBAL_ARGUMENTS=(--profile "$profile" --region "$FOCUS_REGION")
  [[ -z "$auth_mode" ]] || FOCUS_OCI_GLOBAL_ARGUMENTS+=(--auth "$auth_mode")
  if [[ -n "$config_file" ]]; then
    config_file=$(focus_expand_path "$config_file" "$FOCUS_PARAMETER_DIRECTORY")
    [[ -f "$config_file" ]] || focus_die "OciConfigFilePath is not a file: $config_file"
    FOCUS_OCI_GLOBAL_ARGUMENTS+=(--config-file "$config_file")
  fi

  printf 'Checking existing Bastion service in %s...\n' "$FOCUS_REGION"
  bastion_state=$(focus_oci_json bastion bastion get --bastion-id "$FOCUS_BASTION_ID" | jq -r '.data["lifecycle-state"] // empty')
  [[ "$bastion_state" == ACTIVE ]] || focus_die "Bastion $FOCUS_BASTION_ID is ${bastion_state:-UNKNOWN}; expected ACTIVE."

  session_display_name="multiport-${USER:-linux}-$(date -u +%Y%m%dT%H%M%SZ)-$$"
  trap focus_cleanup_bastion_session EXIT INT TERM
  printf 'Creating managed-SSH session %s...\n' "$session_display_name"
  "$FOCUS_OCI_EXECUTABLE" bastion session create-managed-ssh \
    --bastion-id "$FOCUS_BASTION_ID" --display-name "$session_display_name" \
    --key-type PUB --session-ttl "$FOCUS_SESSION_TTL" --ssh-public-key-file "$public_key" \
    --target-os-username "$FOCUS_TARGET_USER" --target-port 22 \
    --target-private-ip "$FOCUS_PRIVATE_IP" --target-resource-id "$FOCUS_INSTANCE_ID" \
    --wait-for-state SUCCEEDED --max-wait-seconds "$FOCUS_WAIT_SECONDS" \
    --wait-interval-seconds "$FOCUS_POLL_SECONDS" "${FOCUS_OCI_GLOBAL_ARGUMENTS[@]}" >/dev/null
  FOCUS_BASTION_SESSION_CREATED=1

  iteration=0
  while (( elapsed < FOCUS_WAIT_SECONDS )); do
    ((iteration += 1))
    session_json=$(focus_oci_json bastion session list --bastion-id "$FOCUS_BASTION_ID" --display-name "$session_display_name" --all)
    FOCUS_BASTION_SESSION_ID=$(jq -r '.data[0].id // empty' <<< "$session_json")
    printf 'Session discovery check %s: %s\n' "$iteration" "${FOCUS_BASTION_SESSION_ID:-not-found}"
    [[ "$FOCUS_BASTION_SESSION_ID" == ocid1.bastionsession.* ]] && break
    sleep "$FOCUS_POLL_SECONDS"; ((elapsed += FOCUS_POLL_SECONDS))
  done
  [[ -n "$FOCUS_BASTION_SESSION_ID" ]] || focus_die "Bastion session was not discoverable within $FOCUS_WAIT_SECONDS seconds."

  iteration=0; elapsed=0; session_state=UNKNOWN
  while (( elapsed < FOCUS_WAIT_SECONDS )); do
    ((iteration += 1))
    session_state=$(focus_oci_json bastion session get --session-id "$FOCUS_BASTION_SESSION_ID" | jq -r '.data["lifecycle-state"] // "UNKNOWN"')
    printf 'Bastion session check %s: status=%s; expected=ACTIVE\n' "$iteration" "$session_state"
    [[ "$session_state" == ACTIVE ]] && break
    [[ "$session_state" != FAILED && "$session_state" != DELETED && "$session_state" != DELETING ]] || focus_die "Bastion session entered terminal state $session_state."
    sleep "$FOCUS_POLL_SECONDS"; ((elapsed += FOCUS_POLL_SECONDS))
  done
  [[ "$session_state" == ACTIVE ]] || focus_die "Bastion session did not reach ACTIVE within $FOCUS_WAIT_SECONDS seconds."

  for iteration in "${!local_ports[@]}"; do
    forward_spec="127.0.0.1:${local_ports[$iteration]}:127.0.0.1:${remote_ports[$iteration]}"
    forward_specs+=("$forward_spec")
    ssh_arguments+=(-L "$forward_spec")
  done
  local bastion_host="host.bastion.$FOCUS_REGION.oci.oraclecloud.com"
  local proxy_command="ssh -i $private_key -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -W %h:%p -p 22 $FOCUS_BASTION_SESSION_ID@$bastion_host"
  ssh_arguments+=(-i "$private_key" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new \
    -o ExitOnForwardFailure=yes -o ServerAliveInterval=120 -o ServerAliveCountMax=3 \
    -o "ProxyCommand=$proxy_command" -p 22 "$FOCUS_TARGET_USER@$FOCUS_PRIVATE_IP")

  printf 'Bastion session is ACTIVE: %s\n' "$FOCUS_BASTION_SESSION_ID"
  printf 'Keep this terminal open. Streamlit: http://127.0.0.1:8501/ and http://127.0.0.1:8502/\n'
  "$FOCUS_SSH_EXECUTABLE" "${ssh_arguments[@]}"
}
