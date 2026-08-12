#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=../scripts/oci-bastion-tunnel-common.sh
source "$ROOT/scripts/oci-bastion-tunnel-common.sh"

parameter_file=""
replace_existing_profile=false
cli_keep_session=false
cli_dry_run=false
while (($#)); do
  case "$1" in
    --parameter-file) [[ $# -ge 2 ]] || focus_die '--parameter-file requires a path'; parameter_file=$2; shift 2 ;;
    --replace-existing-profile) replace_existing_profile=true; shift ;;
    --keep-session) cli_keep_session=true; shift ;;
    --dry-run) cli_dry_run=true; shift ;;
    --help|-h)
      printf 'Usage: %s --parameter-file FILE [--replace-existing-profile] [--keep-session] [--dry-run]\n' "$0"
      exit 0 ;;
    *) focus_die "Unknown argument: $1" ;;
  esac
done
[[ -n "$parameter_file" ]] || focus_die '--parameter-file is required.'

for key in AssetsVersion BastionId InstanceId PrivateIp Region SshPrivateKeyPath SshPublicKeyPath \
  OciUserId OciTenancyId ApiKeyFingerprint ApiPrivateKeyPath ProfileName OciConfigFilePath \
  BastionSessionTtl SshLocalPort OptionalPort1 OptionalPort2 LocalPort RemotePort WaitSeconds \
  PollSeconds TargetUser OciExecutable SshExecutable ReplaceExistingProfile KeepSession DryRun; do
  FOCUS_ALLOWED_SETTINGS[$key]=1
done
focus_load_parameter_file "$parameter_file"

FOCUS_BASTION_ID=$(focus_setting BastionId)
FOCUS_INSTANCE_ID=$(focus_setting InstanceId)
FOCUS_PRIVATE_IP=$(focus_setting PrivateIp)
FOCUS_REGION=$(focus_setting Region)
FOCUS_SSH_PRIVATE_KEY_PATH=$(focus_setting SshPrivateKeyPath)
FOCUS_SSH_PUBLIC_KEY_PATH=$(focus_setting SshPublicKeyPath)
OCI_USER_ID=$(focus_setting OciUserId)
OCI_TENANCY_ID=$(focus_setting OciTenancyId)
API_KEY_FINGERPRINT=$(focus_setting ApiKeyFingerprint)
API_PRIVATE_KEY_PATH=$(focus_setting ApiPrivateKeyPath)
PROFILE_NAME=$(focus_setting ProfileName STREAMLIT_API_KEY)
OCI_CONFIG_FILE_PATH=$(focus_setting OciConfigFilePath "$HOME/.oci/config")
FOCUS_SESSION_TTL=$(focus_integer_setting BastionSessionTtl 3600 30 10800)
FOCUS_SSH_LOCAL_PORT=$(focus_integer_setting SshLocalPort 22 1 65535)
FOCUS_OPTIONAL_PORT1=$(focus_integer_setting OptionalPort1 0 0 65535)
FOCUS_OPTIONAL_PORT2=$(focus_integer_setting OptionalPort2 0 0 65535)
FOCUS_LOCAL_PORT=$(focus_integer_setting LocalPort 0 0 65535)
FOCUS_REMOTE_PORT=$(focus_integer_setting RemotePort 0 0 65535)
FOCUS_WAIT_SECONDS=$(focus_integer_setting WaitSeconds 1200 60 3600)
FOCUS_POLL_SECONDS=$(focus_integer_setting PollSeconds 10 1 60)
FOCUS_TARGET_USER=$(focus_setting TargetUser oracle)
FOCUS_OCI_EXECUTABLE=$(focus_setting OciExecutable oci)
FOCUS_SSH_EXECUTABLE=$(focus_setting SshExecutable ssh)
FOCUS_KEEP_SESSION=$(focus_boolean_setting KeepSession false)
FOCUS_DRY_RUN=$(focus_boolean_setting DryRun false)
file_replace=$(focus_boolean_setting ReplaceExistingProfile false)
[[ "$cli_keep_session" == false ]] || FOCUS_KEEP_SESSION=true
[[ "$cli_dry_run" == false ]] || FOCUS_DRY_RUN=true
[[ "$replace_existing_profile" == false ]] || file_replace=true

for required_pair in \
  "BastionId:$FOCUS_BASTION_ID" "InstanceId:$FOCUS_INSTANCE_ID" "PrivateIp:$FOCUS_PRIVATE_IP" \
  "Region:$FOCUS_REGION" "SshPrivateKeyPath:$FOCUS_SSH_PRIVATE_KEY_PATH" \
  "OciUserId:$OCI_USER_ID" "OciTenancyId:$OCI_TENANCY_ID" \
  "ApiKeyFingerprint:$API_KEY_FINGERPRINT" "ApiPrivateKeyPath:$API_PRIVATE_KEY_PATH"; do
  focus_require_value "${required_pair%%:*}" "${required_pair#*:}"
done
[[ "$FOCUS_BASTION_ID" == ocid1.bastion.* ]] || focus_die 'BastionId must be a Bastion OCID.'
[[ "$FOCUS_INSTANCE_ID" == ocid1.instance.* ]] || focus_die 'InstanceId must be a Compute instance OCID.'
[[ "$OCI_USER_ID" == ocid1.user.* ]] || focus_die 'OciUserId must be an OCI user OCID.'
[[ "$OCI_TENANCY_ID" == ocid1.tenancy.* ]] || focus_die 'OciTenancyId must be an OCI tenancy OCID.'
[[ "$FOCUS_REGION" =~ ^[a-z]{2}-[a-z0-9-]+-[0-9]+$ ]] || focus_die "Invalid OCI region: $FOCUS_REGION"
[[ "$PROFILE_NAME" =~ ^[A-Za-z0-9_-]+$ ]] || focus_die "Invalid ProfileName: $PROFILE_NAME"
[[ "$API_KEY_FINGERPRINT" =~ ^([0-9A-Fa-f]{2}:){15}[0-9A-Fa-f]{2}$ ]] || focus_die 'ApiKeyFingerprint must contain 16 colon-separated hexadecimal bytes.'
focus_validate_ipv4 "$FOCUS_PRIVATE_IP"

API_PRIVATE_KEY_PATH=$(focus_expand_path "$API_PRIVATE_KEY_PATH" "$FOCUS_PARAMETER_DIRECTORY")
OCI_CONFIG_FILE_PATH=$(focus_expand_path "$OCI_CONFIG_FILE_PATH" "$FOCUS_PARAMETER_DIRECTORY")
[[ -f "$API_PRIVATE_KEY_PATH" ]] || focus_die "ApiPrivateKeyPath is not a file: $API_PRIVATE_KEY_PATH"

write_api_profile() {
  local config_file=$1 profile=$2 config_directory temporary_file backup_file timestamp profile_text
  if [[ "$FOCUS_DRY_RUN" == true ]]; then
    printf 'DRY RUN: would configure OCI API-key profile %s in %s.\n' "$profile" "$config_file"
    return
  fi
  config_directory=$(dirname -- "$config_file")
  mkdir -p -- "$config_directory"
  chmod 700 "$config_directory"
  if [[ -f "$config_file" ]] && grep -Eq "^[[:space:]]*\[$profile\][[:space:]]*$" "$config_file"; then
    profile_text=$(awk -v wanted="$profile" '
      /^\[[^]]+\][[:space:]]*$/ {
        name=$0; sub(/^\[/,"",name); sub(/\][[:space:]]*$/,"",name)
        selected=(name==wanted)
        next
      }
      selected { print }
    ' "$config_file")
    if grep -Fxq "user=$OCI_USER_ID" <<< "$profile_text" && \
       grep -Fxq "fingerprint=${API_KEY_FINGERPRINT,,}" <<< "$profile_text" && \
       grep -Fxq "key_file=$API_PRIVATE_KEY_PATH" <<< "$profile_text" && \
       grep -Fxq "tenancy=$OCI_TENANCY_ID" <<< "$profile_text" && \
       grep -Fxq "region=$FOCUS_REGION" <<< "$profile_text"; then
      printf 'OCI profile %s already matches the supplied parameters.\n' "$profile"
      return
    fi
    [[ "$file_replace" == true ]] || focus_die "OCI profile $profile already exists. Review it or use --replace-existing-profile."
  fi
  umask 077
  temporary_file=$(mktemp "$config_directory/.oci-config.XXXXXX")
  if [[ -f "$config_file" ]]; then
    timestamp=$(date -u +%Y%m%dT%H%M%SZ)
    backup_file="$config_file.backup-$timestamp-$$"
    cp -- "$config_file" "$backup_file"
    printf 'Backed up existing OCI configuration: %s\n' "$backup_file"
    awk -v wanted="$profile" '
      /^\[[^]]+\][[:space:]]*$/ {
        name=$0; sub(/^\[/,"",name); sub(/\][[:space:]]*$/,"",name)
        skip=(name==wanted)
      }
      !skip { print }
    ' "$config_file" > "$temporary_file"
  fi
  if [[ -s "$temporary_file" ]]; then printf '\n' >> "$temporary_file"; fi
  {
    printf '[%s]\n' "$profile"
    printf 'user=%s\n' "$OCI_USER_ID"
    printf 'fingerprint=%s\n' "${API_KEY_FINGERPRINT,,}"
    printf 'key_file=%s\n' "$API_PRIVATE_KEY_PATH"
    printf 'tenancy=%s\n' "$OCI_TENANCY_ID"
    printf 'region=%s\n' "$FOCUS_REGION"
  } >> "$temporary_file"
  chmod 600 "$temporary_file"
  mv -f -- "$temporary_file" "$config_file"
  printf 'Configured OCI API-key profile %s in %s\n' "$profile" "$config_file"
}

write_api_profile "$OCI_CONFIG_FILE_PATH" "$PROFILE_NAME"
focus_open_bastion_tunnels '' "$PROFILE_NAME" "$OCI_CONFIG_FILE_PATH"
