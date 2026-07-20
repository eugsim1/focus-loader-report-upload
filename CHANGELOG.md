# Changelog

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
