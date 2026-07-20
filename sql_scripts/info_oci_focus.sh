#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ -f "$script_dir/.env" ]] && { set -a; source "$script_dir/.env"; set +a; }

DB_USER=${DATABASE_USER:-${TARGET_SCHEMA:-}}
DB_PASS=${DATABASE_PASSWORD:-${TARGET_SCHEMA_PASSWORD:-}}
DB_CONN=${DATABASE_NAME:-${DB_TNS_ALIAS:-}}
: "${TNS_ADMIN:?Set TNS_ADMIN to the extracted wallet directory}"
: "${DB_USER:?Set DATABASE_USER or TARGET_SCHEMA}"
: "${DB_PASS:?Set DATABASE_PASSWORD or TARGET_SCHEMA_PASSWORD}"
: "${DB_CONN:?Set DATABASE_NAME or DB_TNS_ALIAS}"
DB_PASS_SQL=${DB_PASS//\"/\"\"}

sqlplus -L -S /nolog <<EOF
SET DEFINE OFF
CONNECT ${DB_USER}/"${DB_PASS_SQL}"@"${DB_CONN}"
set lines 200
col table_name format a30
col partition_name format a30

begin
  dbms_stats.gather_table_stats(
    ownname    => user,
    tabname    => 'TEMP_OCI_FOCUS',
    cascade    => true,
    granularity => 'ALL'
  );
end;
/

select
    table_name,
    count(*) as partition_count,
    sum(nvl(num_rows, 0)) as total_rows
from user_tab_partitions
where table_name = upper('TEMP_OCI_FOCUS')
group by table_name
order by table_name;

select
    partition_position,
    partition_name,
    num_rows
from user_tab_partitions
where table_name = upper('TEMP_OCI_FOCUS')
order by partition_position;
EOF
