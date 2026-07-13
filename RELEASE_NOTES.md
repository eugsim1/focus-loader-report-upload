# Release 26.5.1-go-incremental-upload

## Highlights

- Adds a durable upload-only checkpoint through `-upload-state-file`.
- Keeps upload-only history independent from Oracle load history, allowing an already-loaded source object to be copied to a new destination bucket.
- Retains the immediate one-row latest-upload CSV and full per-run upload results CSV.
- Adds a production-oriented cron wrapper with `flock`, persistent state, OCI Vault support, and explicit Oracle runtime paths.
- Replaces legacy environment-specific helpers with sanitized deployment examples.
- Provides a comprehensive Linux installation, build, configuration, usage, cron, recovery, security, and troubleshooting guide.
- Documents the complete enrichment contract and every command-line flag, including defaults, interactions, and limitations.
- Includes editable and rendered OCI architecture diagrams for Object Storage delivery and Autonomous Database loading.

## Upgrade notes

1. Back up existing `work_report_dir` state and reports.
2. Deploy the new binary together with the updated `focus.conf` and `focus.ctl`.
3. For upload-only schedules, choose a persistent `-upload-state-file`. Use a different state file for every independent destination bucket/prefix.
4. Do not use `-force` for the first upgraded cron run unless every source object must be replayed.
5. Test the cron wrapper twice; the second run should find no pending upload-only objects unless a new source object arrived.

## Compatibility

- Module baseline: Go 1.21.
- Oracle `godror` uses CGO; build with a C compiler.
- Oracle Instant Client Basic is required at runtime.
- Instant Client Tools is required when SQL*Loader is used.
