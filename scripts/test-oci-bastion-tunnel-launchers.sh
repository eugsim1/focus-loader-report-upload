#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
TMP_DIR=$(mktemp -d)
cleanup() { rm -rf -- "$TMP_DIR"; }
trap cleanup EXIT

printf '%s\n' 'TEST PRIVATE KEY' > "$TMP_DIR/ssh_key"
printf '%s\n' 'ssh-ed25519 AAAATEST' > "$TMP_DIR/ssh_key.pub"
printf '%s\n' 'TEST API KEY' > "$TMP_DIR/api_key.pem"

cat > "$TMP_DIR/api-settings.txt" <<EOF
AssetsVersion=1
BastionId=ocid1.bastion.oc1.eu-frankfurt-1.test
InstanceId=ocid1.instance.oc1.eu-frankfurt-1.test
PrivateIp=10.30.1.10
Region=eu-frankfurt-1
SshPrivateKeyPath=$TMP_DIR/ssh_key
SshPublicKeyPath=$TMP_DIR/ssh_key.pub
OciUserId=ocid1.user.oc1..test
OciTenancyId=ocid1.tenancy.oc1..test
ApiKeyFingerprint=aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99
ApiPrivateKeyPath=$TMP_DIR/api_key.pem
OciConfigFilePath=$TMP_DIR/config
SshLocalPort=2222
OptionalPort1=18888
OptionalPort2=19000
EOF

api_output=$(
  "$ROOT/linux-api-key-auth-streamlit/connect-streamlit-api-key-auth.sh" \
    --parameter-file "$TMP_DIR/api-settings.txt" --dry-run 2>&1
)
for expected in \
  '127.0.0.1:2222 -> 127.0.0.1:22' \
  '127.0.0.1:8501 -> 127.0.0.1:8501' \
  '127.0.0.1:8502 -> 127.0.0.1:8502' \
  '127.0.0.1:5901 -> 127.0.0.1:5901' \
  '127.0.0.1:18888 -> 127.0.0.1:18888' \
  '127.0.0.1:19000 -> 127.0.0.1:19000'; do
  grep -Fq -- "$expected" <<< "$api_output" || {
    printf 'Missing API-key dry-run mapping: %s\n%s\n' "$expected" "$api_output" >&2
    exit 1
  }
done
[[ ! -e "$TMP_DIR/config" ]] || {
  printf 'API-key dry run unexpectedly wrote the OCI config.\n' >&2
  exit 1
}

cat > "$TMP_DIR/browser-settings.txt" <<EOF
AssetsVersion=1
BastionId=ocid1.bastion.oc1.eu-frankfurt-1.test
InstanceId=ocid1.instance.oc1.eu-frankfurt-1.test
PrivateIp=10.30.1.10
Region=eu-frankfurt-1
SshPrivateKeyPath=$TMP_DIR/ssh_key
SshPublicKeyPath=$TMP_DIR/ssh_key.pub
OciConfigFilePath=$TMP_DIR/browser-config
SshLocalPort=2223
OptionalPort1=18889
OptionalPort2=19001
EOF

browser_output=$(
  "$ROOT/linux-browser-auth-streamlit/connect-streamlit-browser-auth.sh" \
    --parameter-file "$TMP_DIR/browser-settings.txt" --dry-run 2>&1
)
for expected in \
  '127.0.0.1:2223 -> 127.0.0.1:22' \
  '127.0.0.1:8501 -> 127.0.0.1:8501' \
  '127.0.0.1:8502 -> 127.0.0.1:8502' \
  '127.0.0.1:5901 -> 127.0.0.1:5901' \
  '127.0.0.1:18889 -> 127.0.0.1:18889' \
  '127.0.0.1:19001 -> 127.0.0.1:19001'; do
  grep -Fq -- "$expected" <<< "$browser_output" || {
    printf 'Missing browser-auth dry-run mapping: %s\n%s\n' "$expected" "$browser_output" >&2
    exit 1
  }
done

printf 'OCI Bastion Linux launcher dry-run tests passed.\n'
