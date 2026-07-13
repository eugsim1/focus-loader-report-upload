# Transformed FOCUS CSV Upload Mode

This project can upload the modified CSV generated immediately before SQL*Loader. It does not upload the original `.csv.gz` object.

For every pending FOCUS file, the pipeline is:

```text
Get original .csv.gz from source Object Storage
  -> decompress locally
  -> transform FOCUS columns
  -> resolve compartment paths
  -> parse tags and populate TAG_SPECIAL1..4
  -> write the SQL*Loader-ready .csv
  -> upload that transformed .csv to the destination bucket
  -> optionally run SQL*Loader using that same local .csv
```

The pending set still respects database `LOAD_STATUS`, the restart state file, `-d`, `-f`, and `-force`.

## Flags

| Flag | Description |
| --- | --- |
| `-upload-reports` | Download and transform each pending source gzip, then upload the generated SQL*Loader-ready CSV. SQL*Loader is skipped unless `-load-after-upload` is set. |
| `-report-upload-namespace string` | Destination Object Storage namespace. Defaults to the source namespace from `-ns`. |
| `-report-upload-bucket string` | Destination bucket. Required with `-upload-reports`. |
| `-report-upload-region string` | Destination region. Defaults to the source tenancy home region. |
| `-report-upload-prefix string` | Optional prefix. The source object path is preserved below it, with `.csv.gz` changed to `.csv`. |
| `-focus-upload-results-file string` | Per-file transformed upload results CSV. Default: `work_report_dir/focus_file_upload_results.csv`. |
| `-latest-upload-file string` | Local checkpoint CSV rewritten immediately after every successful upload. Default: `work_report_dir/latest_focus_file_upload.csv`. |
| `-upload-state-file string` | Durable upload-only checkpoint used to skip source files already copied to this destination. Default: `work_report_dir/uploaded_files.jsonl`. |
| `-load-after-upload` | After each transformed CSV uploads successfully, run SQL*Loader using that same local CSV. |
| `-verbose` | Print every transformed upload result. Without it, progress is printed every 100 files and at completion. |

## Destination object names

Without a destination prefix:

```text
Source object:      FOCUS Reports/2025/01/01/example.csv.gz
Transformed upload: FOCUS Reports/2025/01/01/example.csv
```

With `-report-upload-prefix transformed`:

```text
Transformed upload: transformed/FOCUS Reports/2025/01/01/example.csv
```

## Upload only

```bash
./dist/focus-loader-report-upload-linux-amd64 \
  -c ~/.oci/config \
  -t DEFAULT \
  -du ADMIN \
  -dn myadb_high \
  -dp 'password' \
  -ns source_namespace \
  -d 2025-01-01 \
  -workers 5 \
  -upload-reports \
  -report-upload-namespace destination_namespace \
  -report-upload-bucket destination_bucket
```

Upload-only mode automatically enables `-skip-tag-rows` and `-skip-tag-keys`. Tag JSON is still parsed and `TAG_SPECIAL1..4` are still populated in the transformed CSV, but the transformation does not insert row-level tag records or tag-key metadata into ADW. It also does not:

- generate the SQL*Loader control file
- run `sqlldr`
- insert SQL*Loader audit or `LOAD_STATUS` records
- mark the source file as database-loaded in the restart state

Temporary gzip and CSV files are removed after successful upload unless `-keep-work-files` is used.

Upload-only incremental selection uses `-upload-state-file` independently from database load history. Keep a separate checkpoint file for each destination bucket/prefix. Use `-force` only for an intentional replay.

The latest successful upload is always recorded locally in `work_report_dir/latest_focus_file_upload.csv`, even when SQL*Loader is skipped. The checkpoint is updated after the bucket confirms each upload, so it remains useful if the process stops before the final results report is produced. It contains the source object, destination bucket/object, local path, whether the local transformed file was retained, row and byte counts, upload timestamp, ETag, version ID, and request ID. Use `-keep-work-files` when the transformed CSV itself must remain on the local filesystem.

## Upload and then SQL*Loader

```bash
./dist/focus-loader-report-upload-linux-amd64 \
  -c ~/.oci/config \
  -t DEFAULT \
  -du ADMIN \
  -dn myadb_high \
  -dp 'password' \
  -ns source_namespace \
  -d 2025-01-01 \
  -workers 5 \
  -upload-reports \
  -report-upload-namespace destination_namespace \
  -report-upload-bucket destination_bucket \
  -load-after-upload
```

For each worker, SQL*Loader starts only after that file's transformed CSV upload succeeds. An upload failure cancels the worker pool and prevents SQL*Loader for the failed file.

## Results CSV

After the workers stop, the loader writes and uploads `focus_file_upload_results.csv` with:

```text
worker_id,source_object_name,local_csv_path,destination_namespace,destination_bucket,destination_object_name,rows,size_bytes,started_at,ended_at,duration_seconds,status,etag,version_id,opc_request_id,local_file_retained,error
```

The configured OCI principal must be able to read source objects, inspect/read the destination bucket, and create destination objects. A different destination tenancy requires appropriate cross-tenancy policies. Use `-report-upload-region` when the destination bucket is outside the source tenancy home region.

## Pre-load reporting

`-preload-report` and `-skip-preload-content-scan` remain separate, optional report features. They are not required for transformed-file upload.
