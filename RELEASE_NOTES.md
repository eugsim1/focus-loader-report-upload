# Release 26.5.3-tns-gui

## Highlights

- Adds a built-in, read-only browser interface for the first alias in `$TNS_ADMIN/tnsnames.ora`.
- Adds `-tns-gui` and the loopback-only-by-default `-tns-gui-listen` option.
- Adds parser/API tests and Oracle Linux 8 systemd deployment examples.
- Adds a reusable `sql_scripts` package for deploying the FOCUS schema and SQL*Loader audit objects.
- Replaces deployment-specific values with documented environment variables and safe placeholders.
- Includes pre-load and post-load helpers, index maintenance, schema deployment, and OCI FOCUS inspection utilities.
- Adds a sanitization report, security guidance, `.env.example`, and ignore rules for credentials and generated files.
- Retains all application, sanitized SQL, and incremental-upload capabilities from version 26.5.2.

## Upgrade notes

1. Back up existing `work_report_dir` state and reports.
2. Review `sql_scripts/README.md` and copy `sql_scripts/.env.example` to a local, untracked environment file.
3. Supply database credentials and OCI identifiers through the documented environment variables; do not commit them.
4. Validate the scripts against a non-production schema before deployment.
5. Existing 26.5.2 application configuration and checkpoint files remain compatible.
6. For the optional TNS GUI, configure `TNS_ADMIN` in the service environment and follow `README_TNS_GUI.md`.

## Compatibility

- Module baseline: Go 1.21.
- Oracle `godror` uses CGO; build with a C compiler.
- Oracle Instant Client Basic is required at runtime.
- Instant Client Tools is required when SQL*Loader is used.
