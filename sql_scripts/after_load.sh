#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$script_dir/.env" ]] && { set -a; source "$script_dir/.env"; set +a; }

DB_USER=${DB_ADMIN_USER:-ADMIN}
DB_PASS=${DB_ADMIN_PASSWORD:-}
DB_CONN=${DB_TNS_ALIAS:-}
TARGET_SCHEMA=${TARGET_SCHEMA:-}
DB_PASS_SQL=${DB_PASS//\"/\"\"}

if [ -z "$DB_USER" ] || [ -z "$DB_PASS" ] || [ -z "$DB_CONN" ] || [ -z "$TARGET_SCHEMA" ]; then
  echo "Usage: $0 <user> <password> <tns> <schema>"
  exit 1
fi

[[ "$TARGET_SCHEMA" =~ ^[A-Za-z][A-Za-z0-9_\$#]*$ ]] || {
  echo "Invalid TARGET_SCHEMA: $TARGET_SCHEMA" >&2
  exit 1
}

echo "=================================================="
echo "INFO: Re-enabling constraints on TEMP tables"
echo "Schema: $TARGET_SCHEMA"
echo " - Enables PRIMARY KEYS first"
echo " - Then enables FOREIGN KEYS"
echo " - Using NOVALIDATE (no data validation on existing rows)"
echo "=================================================="

sqlplus -L -S /nolog <<EOF

SET DEFINE OFF
CONNECT ${DB_USER}/"${DB_PASS_SQL}"@"${DB_CONN}"

SET SERVEROUTPUT ON
SET ECHO OFF
SET FEEDBACK ON

PROMPT ---- Enabling PRIMARY KEYS in schema ${TARGET_SCHEMA} ----

BEGIN
  FOR c IN (
    SELECT constraint_name, table_name, owner
    FROM all_constraints
    WHERE owner = UPPER('${TARGET_SCHEMA}')
    AND table_name LIKE 'TEMP%'
    AND constraint_type = 'P'
  ) LOOP
    EXECUTE IMMEDIATE 'ALTER TABLE ' || c.owner || '.' || c.table_name ||
                      ' ENABLE NOVALIDATE CONSTRAINT ' || c.constraint_name;

    DBMS_OUTPUT.PUT_LINE('Enabled PK: ' || c.constraint_name || 
                         ' on ' || c.owner || '.' || c.table_name);
  END LOOP;
END;
/

PROMPT ---- Enabling FOREIGN KEYS in schema ${TARGET_SCHEMA} ----

BEGIN
  FOR c IN (
    SELECT constraint_name, table_name, owner
    FROM all_constraints
    WHERE owner = UPPER('${TARGET_SCHEMA}')
    AND table_name LIKE 'TEMP%'
    AND constraint_type = 'R'
  ) LOOP
    EXECUTE IMMEDIATE 'ALTER TABLE ' || c.owner || '.' || c.table_name ||
                      ' ENABLE NOVALIDATE CONSTRAINT ' || c.constraint_name;

    DBMS_OUTPUT.PUT_LINE('Enabled FK: ' || c.constraint_name || 
                         ' on ' || c.owner || '.' || c.table_name);
  END LOOP;
END;
/

BEGIN
  FOR r IN (
    SELECT owner, index_name
    FROM dba_indexes
    WHERE owner = UPPER('${TARGET_SCHEMA}')
      AND status = 'UNUSABLE'
  ) LOOP
    EXECUTE IMMEDIATE 
      'ALTER INDEX ' || r.owner || '.' || r.index_name || ' REBUILD';
  END LOOP;
END;
/

EXIT
EOF
