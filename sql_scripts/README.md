# FOCUS Oracle SQL deployment scripts

Sanitized Oracle Database deployment and maintenance scripts for the FOCUS
loader schema. This distribution contains no credentials, wallet files, OCI
identifiers, or environment-specific database exports.

## Requirements

- Bash 4 or newer
- Oracle Instant Client with SQLPlus
- An extracted Autonomous Database wallet
- OCI CLI only when using `get_oci_focus_tags.sh`

## Configuration

Copy the example locally and edit it. `.env` is ignored by Git.

```bash
chmod +x ./*.sh
cp .env.example .env
chmod 600 .env
set -a
source ./.env
set +a
```

`run_deploy_focus_schema_with_sqlloader_audit.sh` first updates
`sql_scripts/focus.conf`, then copies the completed configuration to
`../focus.conf`. Set `PARENT_CONFIG_FILE=/another/path/focus.conf` to override
the parent destination.

Do not pass passwords as command-line arguments because they can appear in the
process list and shell history.

The Streamlit deployment workflow runs only
`deploy_focus_schema_with_sqlloader_audit.sh` from the installed `sql_scripts`
directory. It sends both passwords through an anonymous inherited file
descriptor rather than command arguments or process-environment values, sets
`FOCUS_IGNORE_DOTENV=true` so a local `.env` cannot override submitted values,
and closes/unsets the credential channel before starting SQL*Plus. The
administrator password is reused through a
short-lived one-use backend authorization; the target-schema password is reused
only to verify and list the created schema's tables.

## Deploy the schema

```bash
./run_deploy_focus_schema_with_sqlloader_audit.sh
```

Set `DROP_EXISTING=true` only when intentionally replacing the target schema.
The default in `.env.example` preserves an existing schema.

## Maintenance

```bash
./before_load.sh
# Run SQL*Loader here.
./after_load.sh
./info_oci_focus.sh
./rebuild_inx.sh
```

## OCI Vault report

Configure `DATABASE_SECRET_ID`, `DATABASE_USER`, `DATABASE_NAME`, and
`OUTPUT_FILE`, then run:

```bash
./get_oci_focus_tags.sh
```

The database password is retrieved from OCI Vault and is not printed. Keep the
OCI configuration and API private key outside this repository.

## Generated files

Column dictionary CSV files, wallet files, `.env`, OCI credentials, logs, and
SQL*Loader outputs are intentionally ignored. Generate a fresh column dictionary
for the selected target schema during deployment.
