# Release 26.5.2-sanitized-sql-distribution

## Highlights

- Adds a reusable `sql_scripts` package for deploying the FOCUS schema and SQL*Loader audit objects.
- Replaces deployment-specific values with documented environment variables and safe placeholders.
- Includes pre-load and post-load helpers, index maintenance, schema deployment, and OCI FOCUS inspection utilities.
- Adds a sanitization report, security guidance, `.env.example`, and ignore rules for credentials and generated files.
- Retains all application and incremental-upload capabilities from version 26.5.1.

## Upgrade notes

1. Back up existing `work_report_dir` state and reports.
2. Review `sql_scripts/README.md` and copy `sql_scripts/.env.example` to a local, untracked environment file.
3. Supply database credentials and OCI identifiers through the documented environment variables; do not commit them.
4. Validate the scripts against a non-production schema before deployment.
5. Existing 26.5.1 application configuration and checkpoint files remain compatible.

## Compatibility

- Module baseline: Go 1.21.
- Oracle `godror` uses CGO; build with a C compiler.
- Oracle Instant Client Basic is required at runtime.
- Instant Client Tools is required when SQL*Loader is used.
