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

sqlplus -L -S /nolog <<EOF

SET DEFINE OFF
CONNECT ${DB_USER}/"${DB_PASS_SQL}"@"${DB_CONN}"

ALTER SESSION ENABLE PARALLEL DML;
ALTER TABLE ${TARGET_SCHEMA}.TEMP_OCI_FOCUS NOLOGGING;

SET SERVEROUTPUT ON
SET ECHO OFF
SET FEEDBACK ON

PROMPT ---- Disabling FK constraints in schema ${TARGET_SCHEMA} ----

BEGIN
  FOR c IN (
    SELECT constraint_name, table_name, owner
    FROM all_constraints
    WHERE owner = UPPER('${TARGET_SCHEMA}')
    AND constraint_type = 'R'
    AND r_constraint_name IN (
      SELECT constraint_name
      FROM all_constraints
      WHERE owner = UPPER('${TARGET_SCHEMA}')
      AND table_name LIKE 'TEMP%'
      AND constraint_type = 'P'
    )
  ) LOOP
    EXECUTE IMMEDIATE 'ALTER TABLE ' || c.owner || '.' || c.table_name ||
                      ' DISABLE CONSTRAINT ' || c.constraint_name;
    DBMS_OUTPUT.PUT_LINE('Disabled FK: ' || c.constraint_name || ' on ' || c.owner || '.' || c.table_name);
  END LOOP;
END;
/

PROMPT ---- Disabling PK constraints in schema ${TARGET_SCHEMA} ----

BEGIN
  FOR c IN (
    SELECT constraint_name, table_name, owner
    FROM all_constraints
    WHERE owner = UPPER('${TARGET_SCHEMA}')
    AND table_name LIKE 'TEMP%'
    AND constraint_type = 'P'
  ) LOOP
    EXECUTE IMMEDIATE 'ALTER TABLE ' || c.owner || '.' || c.table_name ||
                      ' DISABLE CONSTRAINT ' || c.constraint_name;
    DBMS_OUTPUT.PUT_LINE('Disabled PK: ' || c.constraint_name || ' on ' || c.owner || '.' || c.table_name);
  END LOOP;
END;
/

BEGIN
  FOR r IN (
    SELECT owner, index_name
    FROM dba_indexes
    WHERE owner = UPPER('${TARGET_SCHEMA}')
      AND status = 'VALID'
  ) LOOP
    EXECUTE IMMEDIATE 
      'ALTER INDEX ' || r.owner || '.' || r.index_name || ' UNUSABLE';
  END LOOP;
END;
/

EXIT
EOF
