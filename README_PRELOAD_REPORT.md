# FOCUS Loader With Pre-Load Report

This folder contains a copy of the refactored SQL*Loader-based FOCUS loader with added pre-load inspection and capacity-planning reports.

The new report mode lists the pending Object Storage files before the database load starts, downloads each pending gzip stream once for inspection, counts CSV rows, records file sizes, estimates the download time from the observed inspection throughput, and creates an `OCI_FOCUS` capacity-planning CSV from database metadata.

## New Flags

| Flag | Description |
| --- | --- |
| `-preload-report` | Create the pre-load detail and summary CSV reports. The program stops after the report unless `-continue-after-report` is also set. |
| `-preload-report-file string` | Path for the detail CSV report. Default: `work_report_dir/preload_report.csv`. |
| `-continue-after-report` | Continue with the normal database load after creating the pre-load report. |
| `-verbose` | Print one detailed completion line per inspected object. Without this flag, progress is printed every 100 objects and at completion. |
| `-skip-preload-content-scan` | Metadata-only pre-load mode. Do not download objects, decompress gzip streams, or count CSV rows. Used without `-upload-reports`, this automatically enables `-preload-report`; FOCUS object upload already transfers compressed bytes directly. |

The summary and capacity reports are written next to the detail report:

- `work_report_dir/preload_report.csv`
- `work_report_dir/preload_report_summary.csv`
- `work_report_dir/preload_report_capacity.csv`

## Report Columns

Detail report columns:

```text
worker_id,object_name,report_date,time_created,size_bytes,size,inspection_mode,csv_lines_excluding_header,download_inspection_seconds,status,error
```

Summary report columns:

```text
started_at,ended_at,duration_seconds,duration,total_files,successful_files,failed_files,total_bytes,total_size,total_csv_lines_excluding_headers,observed_bytes_per_second,estimated_download_eta_seconds,estimated_download_eta,content_scan_skipped,inspected_bytes
```

Capacity-planning report columns:

```text
section,owner,table_name,partition_name,high_value,current_segment_bytes,current_segment_size,current_stats_rows,partition_stats_rows,incoming_files,incoming_csv_lines,incoming_compressed_bytes,incoming_compressed_size,estimated_bytes_per_existing_row,estimated_incremental_bytes,estimated_incremental_size,projected_segment_bytes,projected_segment_size,projected_rows,last_analyzed,generated_at,notes,error
```

## Important Behavior

`-preload-report` inspects only files that are still pending after the normal filters are applied:

- already loaded files found in the database
- files found in the restart state file
- `-d` minimum file date
- `-f` specific file name
- `-force`, which ignores database and state processed-file skips

When `-continue-after-report` is used, files are streamed once for the report and then downloaded again during the real load. This is intentional because the report must know the row count before SQL*Loader starts.

With `-skip-preload-content-scan`, the report uses Object Storage listing metadata only. Detail rows are marked `METADATA_ONLY`; CSV row counts, measured throughput, download ETA, and row-based capacity projections are unavailable. The capacity CSV leaves those projection fields blank rather than reporting misleading zero values.

With `-verbose`, every completed object prints worker ID, progress, status, size, row count, elapsed time, throughput, and object name. Without it, a progress line is printed every 100 objects and for the final object.

If any file fails during pre-load inspection, the reports are still written, but the run stops with an error instead of continuing to the database load.

The capacity report uses explicitly qualified Oracle data-dictionary views. It compares the configured owner with `SESSION_USER` (not `CURRENT_SCHEMA`) because `ALTER SESSION SET CURRENT_SCHEMA` does not change which objects the `USER_*` views describe:

- `SYS.USER_SEGMENTS` for a table owned by the connected `SESSION_USER`; otherwise `SYS.ALL_SEGMENTS`
- `SYS.USER_TABLES` for a table owned by the connected `SESSION_USER`; otherwise `SYS.ALL_TABLES`
- `SYS.USER_TAB_PARTITIONS` for a table owned by the connected `SESSION_USER`; otherwise `SYS.ALL_TAB_PARTITIONS`

This prevents Oracle from resolving `ALL_SEGMENTS` as an application-schema object such as `FOCUS_TEST_GO_V1.ALL_SEGMENTS`, which caused the previous `ORA-00942` report error.

The incremental capacity estimate is:

```text
estimated_bytes_per_existing_row = current_segment_bytes / current_stats_rows
estimated_incremental_bytes = estimated_bytes_per_existing_row * incoming_csv_lines
projected_segment_bytes = current_segment_bytes + estimated_incremental_bytes
projected_rows = current_stats_rows + incoming_csv_lines
```

For the best estimate, gather table statistics on the FOCUS table before running `-preload-report`.

## Authentication And Database Arguments

Local OCI config profile:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password'
```

Instance principals:

```bash
./usage2adw-go-sqlloader -ip -du ADMIN -dn myadb_high -dp 'password'
```

Database password from OCI Vault secret:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -ds ocid1.vaultsecret.oc1..example
```

Vault secret from another profile:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -ds ocid1.vaultsecret.oc1..example -dst SECRET_PROFILE
```

With proxy:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -p www-proxy-server.com:80 -du ADMIN -dn myadb_high -dp 'password'
```

Show version:

```bash
./usage2adw-go-sqlloader -version
```

## Pre-Load Report Command Combinations

Report only for all pending files:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -preload-report
```

Report only from a minimum FOCUS file date:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -d 2026-06-01 -preload-report
```

Report only for one object name:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -f 'FOCUS Reports/2026/06/01/example.csv.gz' -preload-report
```

Report with a custom output file:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -preload-report -preload-report-file reports/preload_2026_06_01.csv
```

Report using 4 workers:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -workers 4 -preload-report
```

Fast metadata-only report without downloading or decompressing objects:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -skip-preload-content-scan
```

Detailed progress while scanning object contents:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -workers 4 -preload-report -verbose
```

Report, then continue with the database load:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -workers 4 -preload-report -continue-after-report
```

Report and load while ignoring previously processed file skips:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -force -preload-report -continue-after-report
```

Report from a custom Object Storage namespace and bucket:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -ns bling -bn ocid1.tenancy.oc1..example -preload-report
```

## Normal Load Command Combinations

Basic load:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password'
```

Load files from a minimum FOCUS file date:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -d 2026-06-01
```

Load one file:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -f 'FOCUS Reports/2026/06/01/example.csv.gz'
```

Parallel load:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -workers 4
```

Use a custom restart state file:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -state-file work_report_dir/my_processed_files.jsonl
```

Keep downloaded gzip and generated CSV files after successful load:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -keep-work-files
```

Use a custom SQL*Loader control file:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -ctl focus.ctl
```

Force reprocessing:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -force
```

Skip all tag processing:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -skip-tags
```

Skip only row-level tag inserts:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -skip-tag-rows
```

Skip only unique tag key metadata:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -skip-tag-keys
```

Extract special tag keys into `TAG_SPECIAL` columns:

```bash
./usage2adw-go-sqlloader -c ~/.oci/config -t DEFAULT -du ADMIN -dn myadb_high -dp 'password' -ts1 CostCenter -ts2 Application -ts3 Environment -ts4 Owner -ts5 Project
```

## Windows PowerShell Examples

Report only:

```powershell
.\usage2adw-go-sqlloader.exe -c $env:USERPROFILE\.oci\config -t DEFAULT -du ADMIN -dn myadb_high -dp "password" -preload-report
```

Report, then continue:

```powershell
.\usage2adw-go-sqlloader.exe -c $env:USERPROFILE\.oci\config -t DEFAULT -du ADMIN -dn myadb_high -dp "password" -workers 4 -preload-report -continue-after-report
```

Custom report path:

```powershell
.\usage2adw-go-sqlloader.exe -c $env:USERPROFILE\.oci\config -t DEFAULT -du ADMIN -dn myadb_high -dp "password" -preload-report -preload-report-file .\reports\preload.csv
```

## Build

The project uses `github.com/godror/godror`, so building requires CGO and a C compiler in addition to the Go toolchain.

Linux example:

```bash
CGO_ENABLED=1 go build .
```

Windows PowerShell example:

```powershell
$env:CGO_ENABLED="1"
go build .
```

If the build fails with `cgo: C compiler "gcc" not found`, install a C compiler such as GCC or use a build host where CGO is already configured.
