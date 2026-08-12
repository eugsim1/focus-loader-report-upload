#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=../scripts/oci-bastion-tunnel-common.sh
source "$ROOT/scripts/oci-bastion-tunnel-common.sh"

parameter_file=""
cli_use_existing=false
cli_keep_session=false
cli_dry_run=false
while (($#)); do
  case "$1" in
    --parameter-file) [[ $# -ge 2 ]] || focus_die '--parameter-file requires a path'; parameter_file=$2; shift 2 ;;
    --use-existing-session) cli_use_existing=true; shift ;;
    --keep-session) cli_keep_session=true; shift ;;
    --dry-run) cli_dry_run=true; shift ;;
    --help|-h)
      printf 'Usage: %s --parameter-file FILE [--use-existing-session] [--keep-session] [--dry-run]\n' "$0"
      exit 0 ;;
    *) focus_die "Unknown argument: $1" ;;
  esac
done
[[ -n "$parameter_file" ]] || focus_die '--parameter-file is required.'

for key in AssetsVersion BastionId InstanceId PrivateIp Region SshPrivateKeyPath SshPublicKeyPath \
  ProfileName OciConfigFilePath TenancyName IdentityProviderName SessionExpirationMinutes \
  BastionSessionTtl SshLocalPort OptionalPort1 OptionalPort2 LocalPort RemotePort WaitSeconds \
  PollSeconds TargetUser OciExecutable SshExecutable UseExistingSession KeepSession DryRun; do
  FOCUS_ALLOWED_SETTINGS[$key]=1
done
focus_load_parameter_file "$parameter_file"

FOCUS_BASTION_ID=$(focus_setting BastionId)
FOCUS_INSTANCE_ID=$(focus_setting InstanceId)
FOCUS_PRIVATE_IP=$(focus_setting PrivateIp)
FOCUS_REGION=$(focus_setting Region)
FOCUS_SSH_PRIVATE_KEY_PATH=$(focus_setting SshPrivateKeyPath)
FOCUS_SSH_PUBLIC_KEY_PATH=$(focus_setting SshPublicKeyPath)
PROFILE_NAME=$(focus_setting ProfileName BASTION)
OCI_CONFIG_FILE_PATH=$(focus_setting OciConfigFilePath "$HOME/.oci/config")
TENANCY_NAME=$(focus_setting TenancyName)
IDENTITY_PROVIDER_NAME=$(focus_setting IdentityProviderName)
SESSION_EXPIRATION_MINUTES=$(focus_integer_setting SessionExpirationMinutes 60 5 60)
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
FOCUS_USE_EXISTING_SESSION=$(focus_boolean_setting UseExistingSession false)
FOCUS_KEEP_SESSION=$(focus_boolean_setting KeepSession false)
FOCUS_DRY_RUN=$(focus_boolean_setting DryRun false)
[[ "$cli_use_existing" == false ]] || FOCUS_USE_EXISTING_SESSION=true
[[ "$cli_keep_session" == false ]] || FOCUS_KEEP_SESSION=true
[[ "$cli_dry_run" == false ]] || FOCUS_DRY_RUN=true

for required_pair in \
  "BastionId:$FOCUS_BASTION_ID" "InstanceId:$FOCUS_INSTANCE_ID" "PrivateIp:$FOCUS_PRIVATE_IP" \
  "Region:$FOCUS_REGION" "SshPrivateKeyPath:$FOCUS_SSH_PRIVATE_KEY_PATH"; do
  focus_require_value "${required_pair%%:*}" "${required_pair#*:}"
done
[[ "$FOCUS_BASTION_ID" == ocid1.bastion.* ]] || focus_die 'BastionId must be a Bastion OCID.'
[[ "$FOCUS_INSTANCE_ID" == ocid1.instance.* ]] || focus_die 'InstanceId must be a Compute instance OCID.'
[[ "$FOCUS_REGION" =~ ^[a-z]{2}-[a-z0-9-]+-[0-9]+$ ]] || focus_die "Invalid OCI region: $FOCUS_REGION"
[[ "$PROFILE_NAME" =~ ^[A-Za-z0-9_-]+$ ]] || focus_die "Invalid ProfileName: $PROFILE_NAME"
focus_validate_ipv4 "$FOCUS_PRIVATE_IP"
OCI_CONFIG_FILE_PATH=$(focus_expand_path "$OCI_CONFIG_FILE_PATH" "$FOCUS_PARAMETER_DIRECTORY")

if [[ "$FOCUS_DRY_RUN" != true ]]; then
  FOCUS_OCI_EXECUTABLE=$(focus_command_path "$FOCUS_OCI_EXECUTABLE")
  mkdir -p -- "$(dirname -- "$OCI_CONFIG_FILE_PATH")"
  if [[ "$FOCUS_USE_EXISTING_SESSION" != true ]]; then
    authenticate_arguments=(session authenticate --region "$FOCUS_REGION" --profile-name "$PROFILE_NAME" \
      --session-expiration-in-minutes "$SESSION_EXPIRATION_MINUTES" --config-location "$OCI_CONFIG_FILE_PATH")
    [[ -z "$TENANCY_NAME" ]] || authenticate_arguments+=(--tenancy-name "$TENANCY_NAME")
    [[ -z "$IDENTITY_PROVIDER_NAME" ]] || authenticate_arguments+=(--identity-provider-name "$IDENTITY_PROVIDER_NAME")
    printf 'Opening OCI browser authentication for profile %s...\n' "$PROFILE_NAME"
    "$FOCUS_OCI_EXECUTABLE" "${authenticate_arguments[@]}"
  fi
  printf 'Validating OCI browser session profile %s...\n' "$PROFILE_NAME"
  "$FOCUS_OCI_EXECUTABLE" session validate --profile "$PROFILE_NAME" --auth security_token --config-file "$OCI_CONFIG_FILE_PATH"
else
  printf 'DRY RUN: would %s browser-auth profile %s.\n' \
    "$([[ "$FOCUS_USE_EXISTING_SESSION" == true ]] && printf validate || printf create)" "$PROFILE_NAME"
fi

focus_open_bastion_tunnels security_token "$PROFILE_NAME" "$OCI_CONFIG_FILE_PATH"
