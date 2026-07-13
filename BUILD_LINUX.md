# Linux build

## Prerequisites

- Go 1.21 or newer
- A Linux C compiler (`gcc`) because the Oracle `godror` driver uses CGO
- Oracle Instant Client installed on the target Linux host for runtime database access
- SQL*Loader (`sqlldr`) installed and available on `PATH` for actual loads

The executable does not embed Oracle client libraries. At runtime, the `godror` driver loads Oracle Instant Client from the host.

## Build and test on Linux

```bash
go mod download
go test ./...
./build-linux.sh
```

The script writes the executable to `dist/focus-loader-report-upload-linux-amd64` by default. Override the architecture when building natively on the corresponding Linux architecture:

```bash
GOARCH=arm64 ./build-linux.sh
```

## Run the report

Copy `focus.conf`, `focus.ctl`, and the executable to the same working directory, then run:

```bash
./dist/focus-loader-report-upload-linux-amd64 \
  -c "$HOME/.oci/config" \
  -t DEFAULT \
  -du ADMIN \
  -dn myadb_high \
  -dp 'password' \
  -preload-report
```

The capacity output is `work_report_dir/preload_report_capacity.csv` unless `-preload-report-file` selects another base path.

To transform pending FOCUS gzip objects and upload the SQL*Loader-ready CSVs without loading data:

```bash
./dist/focus-loader-report-upload-linux-amd64 \
  -c "$HOME/.oci/config" \
  -t DEFAULT \
  -du ADMIN \
  -dn myadb_high \
  -dp 'password' \
  -ns source_namespace \
  -upload-reports \
  -report-upload-namespace destination_namespace \
  -report-upload-bucket destination_bucket
```

Add `-load-after-upload` to run SQL*Loader for each file immediately after its transformed CSV uploads successfully.
