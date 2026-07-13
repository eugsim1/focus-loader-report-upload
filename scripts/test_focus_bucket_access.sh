#!/usr/bin/env bash

set -euo pipefail

CONFIG_FILE=${OCI_CLI_CONFIG_FILE:-$HOME/.oci/config}
PROFILE=${OCI_CONFIG_PROFILE:-DEFAULT}
REGION=${REGION:-eu-frankfurt-1}
NAMESPACE=${FOCUS_NAMESPACE:-bling}
BUCKET=${FOCUS_BUCKET:-${OCI_TENANCY:-}}
PREFIX=${FOCUS_PREFIX:-FOCUS Reports/}

if ! command -v oci >/dev/null 2>&1; then
  echo "ERROR: oci CLI was not found in PATH" >&2
  exit 1
fi

if [ -z "$BUCKET" ]; then
  echo "ERROR: Set OCI_TENANCY or FOCUS_BUCKET to the customer tenancy OCID." >&2
  exit 1
fi

echo "Testing OCI FOCUS report access"
echo "  Config    : $CONFIG_FILE"
echo "  Profile   : $PROFILE"
echo "  Region    : $REGION"
echo "  Namespace : $NAMESPACE"
echo "  Bucket    : $BUCKET"
echo "  Prefix    : $PREFIX"
echo

common_args=(
  --config-file "$CONFIG_FILE"
  --profile "$PROFILE"
  --region "$REGION"
  --namespace-name "$NAMESPACE"
  --bucket-name "$BUCKET"
  --prefix "$PREFIX"
)

echo "Listing up to five matching objects..."
if ! oci os object list \
    "${common_args[@]}" \
    --limit 5 \
    --fields size,timeCreated \
    --query 'data[].{Name:name,SizeBytes:size,Created:"time-created"}' \
    --output table; then
  cat >&2 <<'POLICY_HELP'

ERROR: The object-list request failed.

For OCI Cost and Usage/FOCUS reports, open Cost and Usage Reports in the
tenancy home region and copy Statement 1 from the OCI Console exactly.
Reporting-tenancy OCIDs can differ. Replace only the group name (and identity
domain when applicable) in Statement 2:

Define tenancy usage-report as <reporting-tenancy-ocid-shown-by-OCI-console>
Endorse group <identity-domain>/<group-name> to read objects in tenancy usage-report

For a group in the default identity domain, this form may be used:

Endorse group <group-name> to read objects in tenancy usage-report

Also verify that the API-key user belongs to that group, that the policy is in
the root compartment, and that REGION is the tenancy home region. IAM policy
changes can take several minutes to become effective.
POLICY_HELP
  exit 2
fi

echo
echo "Counting every matching object; this can take time for a large report set..."
count=$(oci os object list \
  "${common_args[@]}" \
  --all \
  --query 'length(data)' \
  --raw-output)

echo
echo "SUCCESS: Cost/Usage report bucket is accessible."
echo "Matching objects under '$PREFIX': $count"

if [ "$count" = "0" ]; then
  echo "WARNING: Access succeeded, but no objects matched the prefix."
  echo "To test the whole bucket, run with: FOCUS_PREFIX='' $0"
fi
