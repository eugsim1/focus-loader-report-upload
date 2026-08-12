#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d)
trap 'rm -rf -- "$test_dir"' EXIT

cp "$repo_dir/sql_scripts/run_deploy_focus_schema_with_sqlloader_audit.sh" "$test_dir/"
cat >"$test_dir/deploy_focus_schema_with_sqlloader_audit.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$TNS_ADMIN" == "/wallet" ]]
[[ "$DB_ADMIN_USER" == "ADMIN" ]]
[[ "$DB_ADMIN_PASSWORD" == "admin-secret" ]]
[[ "$DB_TNS_ALIAS" == "FOCUS_HIGH" ]]
[[ "$TARGET_SCHEMA" == "FOCUS_APP" ]]
[[ "$TARGET_SCHEMA_PASSWORD" == "schema-secret" ]]
[[ "$DROP_EXISTING" == "true" ]]
printf 'mock deployment succeeded\n'
EOF
printf 'DB_TNS_ALIAS=WRONG_ALIAS\n' >"$test_dir/.env"
chmod 0750 "$test_dir"/*.sh

output=$(
  TNS_ADMIN=/wallet \
  DB_ADMIN_USER=ADMIN \
  DB_TNS_ALIAS=FOCUS_HIGH \
  TARGET_SCHEMA=FOCUS_APP \
  DROP_EXISTING=true \
  FOCUS_IGNORE_DOTENV=true \
  FOCUS_CREDENTIALS_FD=3 \
    "$test_dir/run_deploy_focus_schema_with_sqlloader_audit.sh" \
    3<<<$'admin-secret\nschema-secret'
)

grep -q 'Schema deployment wrapper started' <<<"$output"
grep -q 'mock deployment succeeded' <<<"$output"
if grep -q 'admin-secret\|schema-secret' <<<"$output"; then
  echo "ERROR: deployment wrapper printed a password" >&2
  exit 1
fi
