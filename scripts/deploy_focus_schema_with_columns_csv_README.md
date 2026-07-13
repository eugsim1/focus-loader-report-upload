# FOCUS schema deployment with column CSV

The revised script performs the original schema deployment and then creates a
CSV data dictionary containing one row for every column in the seven deployed
tables.

## Run

```bash
chmod +x deploy_focus_schema_with_columns_csv.sh

./deploy_focus_schema_with_columns_csv.sh \
  <admin_user> \
  <admin_password> \
  <tns_connection> \
  <target_schema> \
  <target_schema_password> \
  [focus.conf]
```

The default CSV filename is:

```text
<TARGET_SCHEMA>_table_columns.csv
```

After updating the selected configuration file, the script copies it to
`../focus.conf`. Override that destination when needed:

```bash
PARENT_CONFIG_FILE=/opt/focus-loader/focus.conf ./deploy_focus_schema_with_columns_csv.sh ...
```

Choose another location with the `COLUMN_CSV` environment variable:

```bash
COLUMN_CSV=/reports/focus_table_columns.csv \
DROP_EXISTING=false \
./deploy_focus_schema_with_columns_csv.sh \
  <admin_user> <admin_password> <tns_connection> \
  <target_schema> <target_schema_password> focus.conf
```

`DROP_EXISTING` still defaults to `true`, exactly as in the original script.
Set it to `false` unless deleting and recreating the entire target schema is
intended.

## CSV fields

The CSV uses `ALL_TAB_COLS` while excluding hidden system columns. It includes
schema, table, column position and name, datatype, byte and
character lengths, length semantics, numeric precision and scale, nullability,
default expression, default-on-null, identity and virtual-column flags,
collation, and table partitioning, compression, logging, and tablespace data.
