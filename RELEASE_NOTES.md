# Release 26.5.4-streamlit

## Highlights

- Adds a separate Streamlit frontend for flexible forms, alias selection, diagnostics, and safe loader-command generation.
- Adds versioned Go health and complete TNS-alias API endpoints for the modular frontend.
- Adds Oracle Linux 8 installer, systemd, smoke-test, upgrade, rollback, and troubleshooting instructions.
- Adds a built-in, read-only browser interface for the first alias in `$TNS_ADMIN/tnsnames.ora`.
- Adds `-tns-gui` and the loopback-only-by-default `-tns-gui-listen` option.
- Adds parser/API tests and Oracle Linux 8 systemd deployment examples.
- Adds a reusable `sql_scripts` package for deploying the FOCUS schema and SQL*Loader audit objects.
- Replaces deployment-specific values with documented environment variables and safe placeholders.
- Includes pre-load and post-load helpers, index maintenance, schema deployment, and OCI FOCUS inspection utilities.
- Adds a sanitization report, security guidance, `.env.example`, and ignore rules for credentials and generated files.
- Retains all application, embedded TNS GUI, sanitized SQL, and incremental-upload capabilities from version 26.5.3.

## Upgrade notes

1. Back up existing `work_report_dir` state and reports.
2. Review `sql_scripts/README.md` and copy `sql_scripts/.env.example` to a local, untracked environment file.
3. Supply database credentials and OCI identifiers through the documented environment variables; do not commit them.
4. Validate the scripts against a non-production schema before deployment.
5. Existing 26.5.3 application configuration and checkpoint files remain compatible.
6. For the embedded TNS GUI and API, configure `TNS_ADMIN` and follow `README_TNS_GUI.md`.
7. For the optional modular frontend, deploy Python 3.11 and follow `streamlit-ui/README.md`.

## Compatibility

- Module baseline: Go 1.21.
- Oracle `godror` uses CGO; build with a C compiler.
- Oracle Instant Client Basic is required at runtime.
- Instant Client Tools is required when SQL*Loader is used.
