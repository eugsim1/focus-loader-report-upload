# Changelog

## 26.9.0-loader-execution-ui

- Added an **Execute loader** Streamlit tab that starts the last validated
  command through an asynchronous, server-controlled Go job API; browser users
  cannot choose the executable or submit shell text.
- Keep `-preload-report`, `-skip-preload-content-scan`, `-skip-tag-rows`, and
  `-continue-after-report` selected by default in both the form and command
  configuration model, with regression tests for all four defaults.
- Added five-second monitoring of `TEMP_OCI_FOCUS`, showing its current row
  count and the increase from the pre-execution baseline while the loader runs.
- Pass direct database passwords to the child loader through standard input
  instead of process arguments; Vault secrets are resolved in backend memory
  for monitoring and are never returned to the browser.
- Limit execution to one job at a time, a 24-hour runtime, validated TNS aliases
  and structured flags, bounded/redacted final output, and a fixed server-side
  executable/working directory.
- Updated the Oracle Linux installer and hardened systemd unit with a dedicated
  writable `work_report_dir`, execution configuration, tests, API reference,
  operations guidance, and security documentation.

## 26.8.1-schema-drop-checkbox

- Removed the typed `DROP <SCHEMA>` confirmation field from the Streamlit
  schema-deployment form.
- Use the single **Drop the existing target schema and all its objects**
  checkbox to request deletion; when selected, the fixed deployment script
  drops the schema and its objects before recreating it.
- Kept protected-schema checks, administrator/target separation, one-use
  authorization, password confirmation, output redaction, and server-controlled
  script execution unchanged.
- Simplified the deployment API client payload and expanded Go/Streamlit tests
  for checkbox-only destructive replacement.

## 26.8.0-command-builder-flags

- Reworked the Streamlit command builder so direct `-dp` database-password
  authentication is the default and OCI Vault `-ds`/`-dst` authentication is
  available as an explicit alternative.
- Added editable inputs for the OCI profile/config, database user and alias,
  namespace, starting date, worker count, exact object, upload destinations, and
  `-ts1` through `-ts4`, with defaults matching the requested pre-load command.
- Replaced the hidden processing-mode mapping with checkboxes for pre-load,
  continuation, metadata-only scan, upload/load, force, tag skips, work-file
  retention, and verbose flags, including CLI-compatible validation.
- Keep submitted direct passwords out of the preview, downloaded file, logs,
  and Streamlit result state; the generated script uses a hidden terminal prompt
  and passes the password through `-dp` only at execution time.
- Expanded command-builder tests and security/operation documentation.

## 26.7.0-schema-deployment-ui

- Added a second Streamlit **Deploy schema** tab that appears only after a
  successful first-tab database login and runs the fixed
  `sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh` server-side script.
- Added 15-minute, random, one-use deployment authorization tokens so Streamlit
  never stores the administrator password, plus masked target-schema password
  inputs, destructive-drop confirmation, password/output redaction, a bounded
  script runtime, and single-deployment concurrency control.
- Reconnect as the created schema with its submitted password after deployment,
  then show and export all resulting tables together with the script exit code,
  timestamps, and console output.
- Updated the Oracle Linux installer and systemd configuration to install the
  fixed script/config, locate SQL*Plus, restrict browser-controlled parameters,
  and grant write access only to the two synchronized `focus.conf` files.
- Expanded Go, Python, installation, API, Bastion, security, and troubleshooting
  coverage for the authenticated deployment workflow.

## 26.6.0-schema-browser

- Added a first Streamlit **Database tables** tab that uses the first TNS alias,
  accepts `ADMIN` or another Oracle user through a masked password field, and
  lists all tables visible for the selected schema.
- Added a loopback-only `POST /api/v1/database/tables` Go endpoint backed by the
  existing `godror` driver and a fixed bind-variable `ALL_TABLES` query; no SQL
  file or caller-supplied SQL is used.
- Clear the password form after submission, redact it from backend errors,
  close each database connection after the lookup, and return only alias,
  login, schema, and owner/table metadata.
- Expanded automated Go/Python tests and Oracle Linux wallet-permission,
  installation, security, and troubleshooting documentation.

## 26.5.12-browser-one-line-command

- Updated the Windows browser-authentication guide with the complete one-line
  `powershell.exe -File` command for the current deployment.
- Included the validated Bastion, Compute, private-IP, SSH-key, region, and
  `BASTION` profile parameters while keeping private-key contents uncommitted.

## 26.5.11-api-key-one-line-command

- Updated the Windows API-key authentication guide with the complete validated
  one-line `powershell.exe -File` command for the current deployment.
- Corrected the Compute instance OCID quoting and retained the warning that the
  referenced private key must remain local and uncommitted.

## 26.5.10-wallet-acl-guidance

- Documented the exact Oracle Linux 8 ACL commands that let `focusloader`
  traverse an Oracle-owned wallet path and read only `tnsnames.ora`.
- Added verification commands and clarified that directory listing remains
  denied intentionally, while full database connections may need separately
  reviewed access to additional wallet files.

## 26.5.9-windows-auth-packages

- Added a standalone Windows browser-authentication launcher that creates or
  reuses an OCI CLI security-token profile before opening the Streamlit tunnel.
- Added a separate Windows API-key launcher that builds a named OCI profile
  from explicit parameters, preserves unrelated profiles, and requires opt-in
  before replacing a different profile.
- Added complete setup, security, one-line invocation, and troubleshooting
  guides, plus automated dry-run and mocked lifecycle coverage for both modes.

## 26.5.8-powershell-json-parsing

- Removed Windows-sensitive JMESPath quoting from the Bastion launcher and now
  parses OCI JSON responses directly in PowerShell.
- Added a complete mocked CI lifecycle covering Bastion lookup, managed-session
  creation and discovery, `ACTIVE` polling, SSH tunnel startup, and cleanup.

## 26.5.7-powershell-oci-errors

- Fixed Windows PowerShell 5.1 handling of OCI CLI stderr so a service error no
  longer terminates the launcher as an opaque `NativeCommandError`.
- Preserved successful OCI warnings while including the complete OCI diagnostic
  in failures, making IAM, region, profile, and OCID problems actionable.

## 26.5.6-powershell-bastion-tunnel

- Added a Windows PowerShell launcher that creates a managed-SSH session on an
  existing OCI Bastion and opens a loopback-only Streamlit tunnel.
- Added Bastion/session state checks, numbered activation polling, local-port
  validation, dry-run support, and automatic cleanup of the created session.
- Documented OCI CLI, OpenSSH, OCID, key, execution-policy, alternate-port, and
  troubleshooting requirements for laptop access.

## 26.5.5-linux8-bastion

- Added a focused Oracle Linux 8 deployment guide for the existing Streamlit interface.
- Documented secure laptop access through an OCI Bastion managed SSH session without exposing ports 8080 or 8501.
- Added automatic GitHub releases for every successfully tested update to `main`, using the newest changelog section as the release notes.

## 26.5.4-streamlit

- Added a separate Streamlit frontend with TNS alias selection, backend diagnostics, and a validated loader-command builder.
- Added versioned Go health and all-alias endpoints while retaining the original embedded-page endpoint.
- Added Oracle Linux 8 installation and smoke-test scripts, hardened systemd deployment assets, and extensive standalone documentation.
- Added Python unit tests and GitHub Actions checks for the Streamlit support modules.

## 26.5.3-tns-gui

- Added `-tns-gui`, a lightweight read-only browser interface compiled into the existing Go binary.
- Added safe parsing of the first alias in `$TNS_ADMIN/tnsnames.ora`, including comments, alias groups, multiline aliases, and `IFILE` skipping.
- Added a loopback-only default listener, JSON status endpoint, request timeouts, browser security headers, and parser/HTTP tests.
- Added Oracle Linux 8 systemd environment/unit examples and a complete deployment, SSH-tunnel, security, and troubleshooting guide.

## 26.5.2-sanitized-sql-distribution

- Added a reusable, sanitized `sql_scripts` deployment package for Oracle schemas and SQL*Loader auditing.
- Added documented environment templates and configuration examples without deployment-specific credentials or OCI identifiers.
- Added security guidance, a sanitization report, and repository safeguards for local secrets and generated artifacts.

## 26.5.1-go-incremental-upload

- Added `-upload-state-file` for durable upload-only restart tracking.
- Successful upload-only files are skipped on later runs unless `-force` is used.
- Added a GitHub-ready Linux operations guide with build, cron, security, recovery, and troubleshooting instructions.
- Expanded the README with the explicit utility goal, detailed Object Storage and Autonomous Database use cases, and behavioral documentation for every CLI flag.
- Added an editable Draw.io architecture diagram and PNG preview using Oracle's official OCI service stencils.

## 26.5.0-go-upload-checkpoint

- Added `-latest-upload-file` and a local one-row CSV checkpoint updated immediately after every successful transformed-file upload.
- Recorded whether the transformed local CSV is retained; use `-keep-work-files` to keep the actual file in upload-only mode.

## 26.4.0-go-transformed-upload

- Changed `-upload-reports` to upload the transformed SQL*Loader-ready CSV instead of the original source gzip object.
- Moved destination upload into the per-file pipeline immediately after `transformFocusCSV` and before control-file generation and SQL*Loader.
- Added safe upload-only behavior that skips tag-table inserts, SQL*Loader, audit/status writes, and database-loaded checkpoints.
- Preserved source object paths while changing destination names from `.csv.gz` to `.csv`.
- Added per-transformed-file rows, sizes, timing, and OCI response metadata to `focus_file_upload_results.csv`.

## 26.3.0-go-focus-upload

- Changed `-upload-reports` to upload the filtered pending FOCUS/FinOps `.csv.gz` objects themselves instead of generated pre-load CSV reports.
- Added parallel streaming `GetObject` to `PutObject` transfer without gzip decompression or CSV row counting.
- Preserved complete source object paths with an optional destination prefix.
- Added destination-region selection and destination-bucket preflight validation.
- Replaced the report-upload audit with per-object `focus_file_upload_results.csv` and uploaded that CSV after transfer.
- Kept SQL*Loader gated on complete object and results-report upload success.

## 26.2.0-go-report-upload

- Added `-verbose` per-object pre-load progress, including worker, size, rows, elapsed time, and throughput.
- Added `-skip-preload-content-scan` metadata-only mode with no source object download, gzip decompression, or CSV row counting.
- Added periodic progress every 100 objects even without verbose mode.
- Marked metadata-only report rows and omitted unavailable row-based capacity projections.

## 26.1.0-go-report-upload

- Added `-upload-reports` upload-only mode for the generated pre-load report set.
- Added destination namespace, bucket, and object-prefix flags.
- Added `-load-after-upload` to gate SQL*Loader on complete upload success.
- Added `preload_report_upload_results.csv` with per-report upload status and OCI response metadata.

## 26.0.2-go-linux

- Fixed `ORA-00942` in the capacity report by using explicit `SYS.USER_*` and `SYS.ALL_*` dictionary view names.
- Fixed schema selection by comparing the table owner with `SESSION_USER`; `CURRENT_SCHEMA` is retained only as the default owner for an unqualified configured table.
- Added regression tests for dictionary-view qualification and schema-selection behavior.
- Added a reproducible Linux build script and Linux deployment notes.
