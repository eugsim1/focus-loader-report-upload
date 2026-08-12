"""Modular Streamlit frontend for the OCI FOCUS Loader."""

from __future__ import annotations

import csv
import io
import os
import re
from datetime import datetime, timezone

import streamlit as st

from command_builder import (
    CommandValidationError,
    LoaderCommandConfig,
    build_loader_command,
    build_loader_execution_payload,
    build_shell_script,
    execution_config_without_password,
)
from focus_api import (
    AliasCatalog,
    DatabaseTable,
    DatabaseTables,
    FocusAPIClient,
    FocusAPIError,
    Health,
    LoaderJob,
    SchemaDeployment,
    SchemaStatistics,
)


EXECUTION_BACKENDS = {
    "focusloader": {
        "api_url": os.getenv(
            "FOCUS_API_URL_FOCUSLOADER",
            os.getenv("FOCUS_API_URL", "http://127.0.0.1:8080"),
        ),
        "executable": os.getenv(
            "FOCUS_LOADER_EXECUTABLE_FOCUSLOADER",
            os.getenv(
                "FOCUS_LOADER_EXECUTABLE",
                "/opt/focus-loader/focus-loader-report-upload",
            ),
        ),
    },
    "oracle": {
        "api_url": os.getenv("FOCUS_API_URL_ORACLE", "http://127.0.0.1:8081"),
        "executable": os.getenv(
            "FOCUS_LOADER_EXECUTABLE_ORACLE",
            "/home/oracle/focus-loader-report-upload/dist/"
            "focus-loader-report-upload-linux-amd64",
        ),
    },
}
ORACLE_IDENTIFIER = re.compile(r"^[A-Za-z][A-Za-z0-9_$#]{0,127}$")
PROTECTED_DEPLOYMENT_SCHEMAS = {"ADMIN", "AUDSYS", "PDBADMIN", "SYS", "SYSTEM"}
GITHUB_RELEASES_URL = (
    "https://github.com/eugsim1/focus-loader-report-upload/releases"
)

st.set_page_config(
    page_title="OCI FOCUS Loader",
    page_icon="📊",
    layout="wide",
    initial_sidebar_state="expanded",
)


def read_backend(api_url: str) -> tuple[Health | None, AliasCatalog | None, str]:
    try:
        client = FocusAPIClient(api_url)
        health = client.health()
        aliases = client.aliases()
        return health, aliases, ""
    except FocusAPIError as error:
        return None, None, str(error)


def render_connection(health: Health, catalog: AliasCatalog) -> str:
    st.subheader("Oracle network connection")
    st.caption(
        "Aliases are read by the Go backend from $TNS_ADMIN/tnsnames.ora. "
        "The Streamlit process does not read the wallet or network configuration."
    )

    metric1, metric2, metric3 = st.columns(3)
    metric1.metric("Backend", health.status.upper())
    metric2.metric("Go version", health.version)
    metric3.metric("TNS aliases", len(catalog.aliases))

    st.text_input("First TNS alias", value=catalog.first_alias, disabled=True)
    selected = st.selectbox(
        "Database service alias",
        options=list(catalog.aliases),
        index=list(catalog.aliases).index(catalog.first_alias),
        help="This value is used as the loader -dn argument in the command builder.",
    )
    st.text_input("Source tnsnames.ora", value=catalog.source_path, disabled=True)
    st.caption(f"Last read from the backend: {catalog.read_at_utc}")

    with st.expander("All aliases"):
        st.dataframe(
            {"Position": list(range(1, len(catalog.aliases) + 1)), "Alias": catalog.aliases},
            hide_index=True,
            use_container_width=True,
        )
    return selected


def tables_csv(tables: tuple[DatabaseTable, ...]) -> str:
    output = io.StringIO(newline="")
    writer = csv.writer(output)
    writer.writerow(["OWNER", "TABLE_NAME"])
    for table in tables:
        writer.writerow([table.owner, table.table_name])
    return output.getvalue()


def schema_deployment_log(result: SchemaDeployment) -> str:
    lines = [
        "FOCUS schema deployment execution log",
        f"Schema: {result.schema}",
        f"TNS alias: {result.connect_alias}",
        f"Administrator: {result.admin_username}",
        f"Script: {result.script_name}",
        f"Working directory: {result.working_directory}",
        f"Drop existing: {str(result.drop_existing).lower()}",
        f"Started UTC: {result.started_at_utc}",
        f"Finished UTC: {result.finished_at_utc}",
        f"Exit code: {result.exit_code}",
        f"Deployment succeeded: {str(result.deployment_succeeded).lower()}",
        f"Output truncated: {str(result.output_truncated).lower()}",
        "",
        "Effective exports and command (passwords redacted):",
        result.command_line,
        "",
        "Combined stdout and stderr:",
        result.output or "(the script returned no console output)",
    ]
    if result.error:
        lines.extend(("", "Backend error:", result.error))
    return "\n".join(lines).rstrip() + "\n"


def loader_execution_log(job: LoaderJob) -> str:
    lines = [
        "FOCUS Loader Streamlit execution log",
        f"Job ID: {job.job_id}",
        f"Status: {job.status}",
        f"Exit code: {job.exit_code if job.exit_code is not None else 'running'}",
        f"Started UTC: {job.started_at_utc or 'not started'}",
        f"Finished UTC: {job.finished_at_utc or 'not finished'}",
        f"Executable: {job.executable}",
        f"Working directory: {job.working_directory}",
        f"Reset work_report_dir: {job.work_report_directory}",
        f"Reset UTC: {job.work_report_reset_at_utc}",
        f"TNS_ADMIN: {job.tns_admin}",
        f"HOME: {job.home_directory}",
        f"Database target: {job.database_user}@{job.database_alias}",
        f"Output truncated: {str(job.output_truncated).lower()}",
        "",
        "Exact backend command (password not included):",
        job.command_line or "(unavailable)",
        "",
        "Backend result:",
        job.error or "(no backend error)",
        "",
        "Combined stdout and stderr:",
        job.output or "(no process output captured yet)",
    ]
    return "\n".join(lines).rstrip() + "\n"


def clear_deployment_authorization() -> None:
    st.session_state.pop("database_deployment_token", None)
    st.session_state.pop("database_deployment_token_expires_at_utc", None)
    st.session_state.pop("database_admin_user", None)


def deployment_authorization_is_active() -> bool:
    token = st.session_state.get("database_deployment_token")
    expires_at = st.session_state.get("database_deployment_token_expires_at_utc")
    if not isinstance(token, str) or not token or not isinstance(expires_at, str):
        return False
    try:
        expiration = datetime.fromisoformat(expires_at.replace("Z", "+00:00"))
    except ValueError:
        clear_deployment_authorization()
        return False
    if expiration.tzinfo is None or expiration <= datetime.now(timezone.utc):
        clear_deployment_authorization()
        return False
    return True


def render_database_tables(api_url: str, catalog: AliasCatalog) -> None:
    st.subheader("Database tables")
    st.caption(
        "Connect with the first TNS alias and list tables visible for one schema. "
        "The query is fixed and read-only; no SQL file is required."
    )

    st.text_input(
        "First TNS alias used for this connection",
        value=catalog.first_alias,
        disabled=True,
    )
    database_user = st.text_input(
        "Database user",
        value="ADMIN",
        help="Use ADMIN or another unquoted Oracle database user.",
        key="schema_browser_user",
    ).strip()
    use_login_schema = st.checkbox(
        "List the login user's schema", value=True, key="schema_browser_use_login_schema"
    )
    if use_login_schema:
        schema_owner = database_user.upper()
        st.caption("Schema owner")
        st.code(schema_owner or "Enter a database user", language=None)
    else:
        schema_owner = st.text_input(
            "Schema owner",
            value="FOCUS_APP",
            help="ADMIN can use this field to inspect another accessible schema.",
            key="schema_browser_schema",
        ).strip().upper()

    with st.form("database-table-lookup", clear_on_submit=True):
        database_password = st.text_input(
            "Database password",
            type="password",
            help="Used only for this connection attempt; never logged or returned by the API.",
            key="schema_browser_password",
        )
        submitted = st.form_submit_button("Connect and list tables", type="primary")

    if submitted:
        st.session_state.pop("database_tables_result", None)
        st.session_state.pop("database_tables_error", None)
        st.session_state.pop("schema_deployment_result", None)
        clear_deployment_authorization()
        if not database_user:
            st.session_state["database_tables_error"] = "Database user is required."
        elif not schema_owner:
            st.session_state["database_tables_error"] = "Schema owner is required."
        elif not database_password:
            st.session_state["database_tables_error"] = "Database password is required."
        else:
            try:
                with st.spinner(f"Connecting to {catalog.first_alias}..."):
                    result = FocusAPIClient(api_url, timeout_seconds=35.0).schema_tables(
                        database_user,
                        database_password,
                        schema_owner,
                    )
                st.session_state["database_tables_result"] = result
                st.session_state["database_deployment_token"] = result.deployment_token
                st.session_state["database_deployment_token_expires_at_utc"] = (
                    result.deployment_token_expires_at_utc
                )
                st.session_state["database_admin_user"] = result.username
                st.rerun()
            except FocusAPIError as error:
                st.session_state["database_tables_error"] = str(error)

    if error := st.session_state.get("database_tables_error"):
        st.error(error)

    result = st.session_state.get("database_tables_result")
    if isinstance(result, DatabaseTables):
        st.success(
            f"Connected to {result.connect_alias} as {result.username}. "
            f"Found {len(result.tables)} table(s) in {result.schema}."
        )
        st.dataframe(
            [
                {"Position": index, "Owner": table.owner, "Table": table.table_name}
                for index, table in enumerate(result.tables, start=1)
            ],
            hide_index=True,
            use_container_width=True,
        )
        st.download_button(
            "Download table list as CSV",
            data=tables_csv(result.tables),
            file_name=f"{result.schema.lower()}-tables.csv",
            mime="text/csv",
        )
        if st.button("Clear database result"):
            st.session_state.pop("database_tables_result", None)
            st.session_state.pop("database_tables_error", None)
            st.session_state.pop("schema_deployment_result", None)
            clear_deployment_authorization()
            st.rerun()

    st.caption(
        "The backend opens one connection for this request and closes it after "
        "the metadata query. The password field clears after submission."
    )


def render_schema_deployment(api_url: str, catalog: AliasCatalog) -> None:
    st.subheader("Deploy FOCUS schema")
    st.caption(
        "Runs the fixed sql_scripts/run_deploy_focus_schema_with_sqlloader_audit.sh "
        "file on the Linux server. TNS_ADMIN and the first TNS alias are loaded "
        "by the Go backend and cannot be overridden in the browser."
    )

    result = st.session_state.get("schema_deployment_result")
    authorization_active = deployment_authorization_is_active()
    if authorization_active:
        st.success(
            "Administrator login verified. This one-use deployment authorization "
            "expires at "
            f"{st.session_state['database_deployment_token_expires_at_utc']}."
        )
        with st.form("schema-deployment", clear_on_submit=True):
            st.text_input(
                "Administrator user",
                value=st.session_state.get("database_admin_user", ""),
                disabled=True,
            )
            st.text_input("TNS alias", value=catalog.first_alias, disabled=True)
            st.text_input(
                "Deployment script",
                value="sql_scripts/run_deploy_focus_schema_with_sqlloader_audit.sh",
                disabled=True,
            )
            target_schema = st.text_input(
                "Target schema",
                value="FOCUS_APP",
                help="Use an unquoted Oracle schema name.",
            ).strip().upper()
            target_password = st.text_input(
                "Target schema password",
                type="password",
                help=(
                    "Used by the deployment script and then immediately reused to "
                    "connect as the created schema and list its tables."
                ),
            )
            confirm_password = st.text_input(
                "Confirm target schema password",
                type="password",
            )
            drop_existing = st.checkbox(
                "Drop the existing target schema and all its objects",
                value=False,
                help=(
                    "When selected, the deployment script deletes the existing schema "
                    "and all its objects before creating it again."
                ),
            )
            submitted = st.form_submit_button("Run schema deployment", type="primary")

        if submitted:
            validation_error = ""
            if not target_schema:
                validation_error = "Target schema is required."
            elif not ORACLE_IDENTIFIER.fullmatch(target_schema):
                validation_error = "Target schema must be an unquoted Oracle identifier."
            elif target_schema in PROTECTED_DEPLOYMENT_SCHEMAS:
                validation_error = "That protected database schema cannot be deployed here."
            elif target_schema == str(
                st.session_state.get("database_admin_user", "")
            ).upper():
                validation_error = "Target schema cannot be the administrator login user."
            elif not target_password:
                validation_error = "Target schema password is required."
            elif target_password != confirm_password:
                validation_error = "The target schema passwords do not match."

            if validation_error:
                st.error(validation_error)
            else:
                deployment_token = st.session_state["database_deployment_token"]
                try:
                    with st.spinner(
                        f"Running {target_schema} deployment through {catalog.first_alias}..."
                    ):
                        result = FocusAPIClient(
                            api_url, timeout_seconds=630.0
                        ).deploy_schema(
                            deployment_token,
                            target_schema,
                            target_password,
                            drop_existing,
                        )
                    st.session_state["schema_deployment_result"] = result
                except FocusAPIError as error:
                    st.error(str(error))
                finally:
                    clear_deployment_authorization()
    elif not isinstance(result, SchemaDeployment):
        st.info(
            "Connect successfully in the Database tables tab first. The deployment "
            "tab is unlocked by a short-lived, one-use authorization."
        )

    result = st.session_state.get("schema_deployment_result")
    if not isinstance(result, SchemaDeployment):
        return

    if result.deployment_succeeded and result.table_lookup_succeeded:
        st.success(
            f"Schema {result.schema} was deployed and verified through "
            f"{result.connect_alias}. Found {len(result.tables)} table(s)."
        )
    elif result.deployment_succeeded:
        st.warning(result.error or "The schema deployed, but table verification failed.")
    else:
        st.error(result.error or "The schema deployment failed.")

    metric1, metric2, metric3 = st.columns(3)
    metric1.metric("Exit code", result.exit_code)
    metric2.metric("Deployment", "SUCCESS" if result.deployment_succeeded else "FAILED")
    metric3.metric("Created-schema tables", len(result.tables))
    st.caption(
        f"Script: {result.script_name} | Started: {result.started_at_utc} | "
        f"Finished: {result.finished_at_utc}"
    )
    st.text_area(
        "Effective exports and command (passwords redacted)",
        value=result.command_line,
        height=260,
        disabled=True,
    )
    if result.output_truncated:
        st.warning(
            "The backend reached its deployment-output safety limit. The visible "
            "output and downloaded log are marked as truncated."
        )
    st.text_area(
        "Deployment combined stdout and stderr",
        value=result.output or "(the script returned no console output)",
        height=320,
        disabled=True,
    )
    log_timestamp = re.sub(r"[^0-9A-Za-z]+", "-", result.started_at_utc).strip("-")
    st.download_button(
        "Download deployment execution log",
        data=schema_deployment_log(result),
        file_name=(
            f"{result.schema.lower()}-schema-deployment-"
            f"{log_timestamp or 'execution'}.log"
        ),
        mime="text/plain",
    )
    if result.tables:
        st.dataframe(
            [
                {"Position": index, "Owner": table.owner, "Table": table.table_name}
                for index, table in enumerate(result.tables, start=1)
            ],
            hide_index=True,
            use_container_width=True,
        )
        st.download_button(
            "Download created-schema table list as CSV",
            data=tables_csv(result.tables),
            file_name=f"{result.schema.lower()}-deployed-tables.csv",
            mime="text/csv",
        )
    st.info("Authenticate again in the first tab before running another deployment.")
    if st.button("Clear deployment result"):
        st.session_state.pop("schema_deployment_result", None)
        st.rerun()


def render_schema_statistics(api_url: str, catalog: AliasCatalog) -> None:
    st.subheader("Schema statistics")
    st.caption(
        "Checks the fixed TEMP_OCI_FOCUS table through the first TNS alias. "
        "The backend runs only fixed, read-only SQL and closes the database "
        "connection after this request."
    )
    database_user = st.text_input(
        "Statistics database user",
        value="ADMIN",
        key="schema_stats_user",
        help="Use the schema login itself, ADMIN, or another authorized user.",
    ).strip()
    use_login_schema = st.checkbox(
        "Check the login user's schema",
        value=True,
        key="schema_stats_use_login_schema",
    )
    if use_login_schema:
        schema_owner = database_user.upper()
        st.caption("Schema owner")
        st.code(schema_owner or "Enter a database user", language=None)
    else:
        schema_owner = st.text_input(
            "Statistics schema owner",
            value="FOCUS_APP",
            key="schema_stats_schema",
            help="The schema containing TEMP_OCI_FOCUS.",
        ).strip().upper()

    with st.form("schema-statistics", clear_on_submit=True):
        database_password = st.text_input(
            "Statistics database password",
            type="password",
            help="Used only for this read-only request and never stored by Streamlit.",
        )
        submitted = st.form_submit_button(
            "Check schema statistics", type="primary"
        )

    if submitted:
        st.session_state.pop("schema_stats_result", None)
        st.session_state.pop("schema_stats_error", None)
        if not database_user:
            st.session_state["schema_stats_error"] = "Database user is required."
        elif not schema_owner:
            st.session_state["schema_stats_error"] = "Schema owner is required."
        elif not ORACLE_IDENTIFIER.fullmatch(database_user):
            st.session_state["schema_stats_error"] = (
                "Database user must be an unquoted Oracle identifier."
            )
        elif not ORACLE_IDENTIFIER.fullmatch(schema_owner):
            st.session_state["schema_stats_error"] = (
                "Schema owner must be an unquoted Oracle identifier."
            )
        elif not database_password:
            st.session_state["schema_stats_error"] = "Database password is required."
        else:
            try:
                with st.spinner(
                    f"Checking {schema_owner}.TEMP_OCI_FOCUS through "
                    f"{catalog.first_alias}..."
                ):
                    result = FocusAPIClient(
                        api_url, timeout_seconds=35.0
                    ).schema_statistics(
                        database_user,
                        database_password,
                        schema_owner,
                    )
                st.session_state["schema_stats_result"] = result
            except FocusAPIError as error:
                st.session_state["schema_stats_error"] = str(error)

    if error := st.session_state.get("schema_stats_error"):
        st.error(error)

    result = st.session_state.get("schema_stats_result")
    if not isinstance(result, SchemaStatistics):
        return

    table_column, data_column, rows_column, loaded_column = st.columns(4)
    table_column.metric("TEMP_OCI_FOCUS exists", "Yes" if result.table_exists else "No")
    data_column.metric("Table has data", "Yes" if result.has_data else "No")
    rows_column.metric("Total rows", f"{result.total_rows:,}")
    loaded_column.metric("Last LOAD_DATE", result.last_load_date or "No loaded rows")
    st.caption(
        f"Schema {result.schema}; login {result.username}; alias "
        f"{result.connect_alias}; checked at {result.queried_at_utc}. "
        "LOAD_DATE is displayed in the Oracle database session time."
    )

    if not result.table_exists:
        st.warning(f"{result.schema}.{result.table_name} does not exist or is not visible.")
    elif not result.has_data:
        st.info(f"{result.schema}.{result.table_name} exists but contains no rows.")

    st.markdown(
        f"#### Current-month effective cost ({result.current_month or 'unavailable'})"
    )
    if result.current_month_costs:
        st.dataframe(
            [
                {
                    "Billing currency": cost.billing_currency,
                    "Total effective cost": cost.effective_cost,
                }
                for cost in result.current_month_costs
            ],
            hide_index=True,
            use_container_width=True,
        )
    elif result.table_exists:
        st.info("No CHARGE_PERIOD_START rows exist for the current database month.")

    if st.button("Clear schema statistics"):
        st.session_state.pop("schema_stats_result", None)
        st.session_state.pop("schema_stats_error", None)
        st.rerun()


def clear_execution_user_context() -> None:
    """Discard state that belongs to the previously selected Unix backend."""
    for key in (
        "database_tables_result",
        "database_tables_error",
        "database_deployment_token",
        "database_deployment_token_expires_at_utc",
        "database_admin_user",
        "schema_deployment_result",
        "schema_stats_result",
        "schema_stats_error",
        "loader_command",
        "loader_script",
        "loader_execution_config",
        "loader_job_id",
        "loader_job_last",
    ):
        st.session_state.pop(key, None)


def render_command_builder(
    selected_alias: str, default_executable: str, execution_user: str
) -> None:
    st.subheader("Loader command builder")
    st.warning(
        "This page validates and previews a command. Use the separate Execute "
        "loader tab to run the validated settings through the fixed backend binary. "
        "The downloaded direct-password script prompts securely at runtime so "
        "the entered password is never displayed or saved. During execution, "
        "-dp may still be visible to same-host process inspection; prefer Vault "
        "for scheduled or shared systems."
    )

    with st.form("loader-command-builder", clear_on_submit=True):
        authentication, source, tags, destination, flags = st.tabs(
            ["Authentication", "Source", "Tag fields", "Destination", "Flags"]
        )
        with authentication:
            executable = st.text_input(
                "Loader executable",
                value=default_executable,
                key=f"loader_executable_{execution_user}",
                help=(
                    "Preview/download path for the selected Unix user. Actual GUI "
                    "execution remains fixed by that user's Go backend service."
                ),
            )
            oci_auth_mode = st.radio(
                "OCI authentication",
                ["OCI config/profile", "Instance principal"],
                horizontal=True,
                help=(
                    "Config/profile emits -t and optional -c. Instance principal "
                    "emits -ip."
                ),
            )
            oci_config_file = st.text_input(
                "OCI config file (-c)",
                placeholder="Optional, for example /home/focusloader/.oci/config",
            )
            oci_profile = st.text_input("OCI profile (-t)", value="DEFAULT")
            database_auth_mode = st.radio(
                "Database authentication",
                ["Database password", "OCI Vault secret"],
                horizontal=True,
                help="Direct database password is selected by default.",
            )
            database_user = st.text_input("Database user (-du)", value="FOCUS_GIT1")
            database_alias = st.text_input("Database alias (-dn)", value=selected_alias)
            database_password = st.text_input(
                "Database password (-dp)",
                type="password",
                help=(
                    "Required when Database password is selected. It is validated "
                    "but never placed in the preview, downloaded file, logs, or "
                    "Streamlit session result; the downloaded script prompts again."
                ),
            )
            vault_secret_ocid = st.text_input(
                "OCI Vault secret OCID (-ds)",
                placeholder="ocid1.vaultsecret.oc1..replace_me",
                help="Used only when OCI Vault secret is selected.",
            )
            vault_secret_profile = st.text_input(
                "Vault secret profile (-dst)",
                placeholder="Optional; blank/local uses instance principal",
            )

        with source:
            source_namespace = st.text_input("Source namespace (-ns)", value="bling")
            source_bucket = st.text_input("Source bucket (-bn)", placeholder="Optional")
            minimum_date = st.text_input("Starting date (-d)", value="2026-01-01")
            exact_object = st.text_input(
                "Exact object (-f)", placeholder="FOCUS Reports/YYYY/MM/DD/file.csv.gz"
            )
            workers = st.number_input(
                "Number of workers (-workers)", min_value=1, max_value=128, value=5, step=1
            )

        with tags:
            tag_special1 = st.text_input(
                "Special tag 1 (-ts1)", value="Oracle-Tags.CreatedBy"
            )
            tag_special2 = st.text_input(
                "Special tag 2 (-ts2)", value="CCA_Basic_Tag.email"
            )
            tag_special3 = st.text_input(
                "Special tag 3 (-ts3)", value="Oracle_Tags.CreatedBy"
            )
            tag_special4 = st.text_input(
                "Special tag 4 (-ts4)", value="Oracle_Tags.CreatedOn"
            )

        with destination:
            destination_namespace = st.text_input(
                "Destination namespace (-report-upload-namespace)", placeholder="Optional"
            )
            destination_bucket = st.text_input(
                "Destination bucket (-report-upload-bucket)", placeholder="Required with upload"
            )
            destination_prefix = st.text_input(
                "Destination prefix (-report-upload-prefix)", value="transformed-focus"
            )

        with flags:
            left, right = st.columns(2)
            with left:
                preload_report = st.checkbox("Pre-load report (-preload-report)", value=True)
                continue_after_report = st.checkbox(
                    "Continue after report (-continue-after-report)", value=True
                )
                skip_preload_content_scan = st.checkbox(
                    "Skip pre-load content scan (-skip-preload-content-scan)", value=True
                )
                upload_reports = st.checkbox("Upload reports (-upload-reports)")
                load_after_upload = st.checkbox("Load after upload (-load-after-upload)")
                force = st.checkbox("Force controlled replay (-force)")
            with right:
                skip_tags = st.checkbox("Skip all tag processing (-skip-tags)")
                skip_tag_rows = st.checkbox("Skip tag rows (-skip-tag-rows)", value=True)
                skip_tag_keys = st.checkbox("Skip tag keys (-skip-tag-keys)")
                keep_work_files = st.checkbox("Keep work files (-keep-work-files)")
                verbose = st.checkbox("Verbose diagnostics (-verbose)")

        submitted = st.form_submit_button("Validate and build command", type="primary")

    if submitted:
        config = LoaderCommandConfig(
            executable=executable.strip(),
            oci_auth_mode=oci_auth_mode,
            database_auth_mode=database_auth_mode,
            database_user=database_user.strip(),
            database_alias=database_alias.strip(),
            database_password=database_password,
            vault_secret_ocid=vault_secret_ocid.strip(),
            vault_secret_profile=vault_secret_profile.strip(),
            source_namespace=source_namespace.strip(),
            source_bucket=source_bucket.strip(),
            oci_config_file=oci_config_file.strip(),
            oci_profile=oci_profile.strip(),
            minimum_date=minimum_date.strip(),
            workers=int(workers),
            exact_object=exact_object.strip(),
            tag_special1=tag_special1.strip(),
            tag_special2=tag_special2.strip(),
            tag_special3=tag_special3.strip(),
            tag_special4=tag_special4.strip(),
            destination_namespace=destination_namespace.strip(),
            destination_bucket=destination_bucket.strip(),
            destination_prefix=destination_prefix.strip(),
            preload_report=preload_report,
            continue_after_report=continue_after_report,
            skip_preload_content_scan=skip_preload_content_scan,
            upload_reports=upload_reports,
            load_after_upload=load_after_upload,
            force=force,
            skip_tags=skip_tags,
            skip_tag_rows=skip_tag_rows,
            skip_tag_keys=skip_tag_keys,
            keep_work_files=keep_work_files,
            verbose=verbose,
        )
        try:
            st.session_state["loader_command"] = build_loader_command(config)
            st.session_state["loader_script"] = build_shell_script(config)
            st.session_state["loader_execution_config"] = (
                execution_config_without_password(config)
            )
            st.success("Command validation passed.")
        except CommandValidationError as error:
            st.session_state.pop("loader_command", None)
            st.session_state.pop("loader_script", None)
            st.session_state.pop("loader_execution_config", None)
            st.error(str(error))

    if command := st.session_state.get("loader_command"):
        st.caption(
            "[DATABASE_PASSWORD_PROMPT] is deliberately redacted. The downloaded "
            "script requests the password with a hidden terminal prompt."
        )
        st.code(command, language="bash", wrap_lines=True)
        st.download_button(
            "Download reviewed command",
            data=st.session_state["loader_script"],
            file_name="run-focus-loader.sh",
            mime="text/x-shellscript",
        )


def render_loader_job(job: LoaderJob) -> None:
    status_column, inserted_column, total_column = st.columns(3)
    status_column.metric("Job status", job.status.replace("_", " ").title())
    inserted_column.metric(
        "Rows inserted this run",
        f"{job.rows_inserted:,}" if job.rows_inserted_known else "Waiting...",
    )
    total_column.metric(
        f"{job.table_name} total rows",
        f"{job.current_row_count:,}" if job.current_row_count_known else "Waiting...",
    )

    if job.row_count_updated_at_utc:
        st.caption(
            f"Row count last updated at {job.row_count_updated_at_utc}; "
            f"baseline={job.initial_row_count if job.initial_row_count_known else 'unavailable'}."
        )
    if job.row_count_error:
        st.warning(
            "The loader job is still monitored, but the latest table row-count query "
            f"failed: {job.row_count_error}"
        )
    if job.status in {"starting", "running"}:
        st.info(
            "Execution is active. This panel refreshes every five seconds. "
            "Committed rows become visible after each SQL*Loader transaction."
        )
    elif job.status == "succeeded":
        st.success(f"Loader execution completed with exit code {job.exit_code}.")
    else:
        st.error(job.error or f"Loader execution ended with status {job.status}.")

    diagnostics_expanded = job.status in {"failed", "timed_out"}
    with st.expander("Execution command and diagnostics", expanded=diagnostics_expanded):
        st.json(
            {
                "jobId": job.job_id,
                "status": job.status,
                "exitCode": job.exit_code,
                "startedAtUtc": job.started_at_utc,
                "finishedAtUtc": job.finished_at_utc,
                "executable": job.executable,
                "workingDirectory": job.working_directory,
                "TNS_ADMIN": job.tns_admin,
                "HOME": job.home_directory,
                "PATH": job.path_environment,
                "workReportDirectory": job.work_report_directory,
                "workReportResetAtUtc": job.work_report_reset_at_utc,
                "databaseUser": job.database_user,
                "databaseAlias": job.database_alias,
                "outputTruncated": job.output_truncated,
            }
        )
        if job.command_line:
            st.markdown("#### Exact backend command")
            st.caption(
                "This is the executable and argument list used by the Go backend. "
                "With database-password authentication, -dp-stdin receives the password "
                "through standard input; the password is never displayed."
            )
            st.code(job.command_line, language="bash", wrap_lines=True)
        if job.manual_command:
            st.markdown("#### Copy/paste command for a manual test")
            st.caption(
                "Run this one-line Bash command as the selected Linux execution user. "
                "It changes to the backend working directory and securely prompts for "
                "the database password when required."
            )
            st.code(job.manual_command, language="bash", wrap_lines=True)

    st.markdown("#### Full loader execution log")
    st.caption(
        "The backend streams combined stdout and stderr into this panel while the "
        "process runs. The log also contains process paths, the redacted command, "
        "work_report_dir reset details, exit code, and the complete backend error."
    )
    if job.output_truncated:
        st.warning(
            "The loader produced more than 8 MiB of console output. The API response "
            "contains the bounded capture and marks it as truncated."
        )
    complete_log = loader_execution_log(job)
    st.text_area(
        "Loader stdout, stderr, and process diagnostics",
        value=complete_log,
        height=600,
        disabled=True,
        key=f"loader-output-{job.job_id}-{job.status}-{len(complete_log)}",
    )
    st.download_button(
        "Download full loader execution log",
        data=complete_log,
        file_name=f"focus-loader-{job.job_id[:12]}-{job.status}.log",
        mime="text/plain",
        key=f"loader-log-download-{job.job_id}-{job.status}-{len(complete_log)}",
    )


def render_loader_execution(api_url: str) -> None:
    st.subheader("Execute loader")
    st.caption(
        "Runs only the backend-configured FOCUS Loader executable. The browser "
        "submits validated fields, never a shell command or executable path."
    )

    active_job_id = st.session_state.get("loader_job_id")
    if isinstance(active_job_id, str) and active_job_id:

        @st.fragment(run_every="5s")
        def poll_loader_job() -> None:
            try:
                job = FocusAPIClient(api_url, timeout_seconds=15.0).loader_job(
                    active_job_id
                )
            except FocusAPIError as error:
                st.error(str(error))
                return
            st.session_state["loader_job_last"] = job
            render_loader_job(job)
            if job.terminal:
                st.session_state.pop("loader_job_id", None)
                st.rerun()

        poll_loader_job()
        return

    last_job = st.session_state.get("loader_job_last")
    if isinstance(last_job, LoaderJob):
        render_loader_job(last_job)
        if st.button("Clear completed execution result"):
            st.session_state.pop("loader_job_last", None)
            st.rerun()
        st.divider()

    config = st.session_state.get("loader_execution_config")
    if not isinstance(config, LoaderCommandConfig):
        st.info(
            "Validate a command in the Command builder tab first. Its non-secret "
            "settings will then become available here."
        )
        return

    st.markdown("#### Validated command preview")
    st.code(st.session_state.get("loader_command", ""), language="bash", wrap_lines=True)
    st.caption(
        "After the job starts, this tab displays the exact backend command and a "
        "copy/paste one-line command for reproducing the execution manually."
    )
    st.caption(
        f"Target: {config.database_user}@{config.database_alias}; "
        f"workers={config.workers}; authentication={config.database_auth_mode}."
    )
    with st.form("loader-execution", clear_on_submit=True):
        runtime_password = ""
        if config.database_auth_mode == "Database password":
            runtime_password = st.text_input(
                "Database password for execution",
                type="password",
                help=(
                    "Submitted once to the loopback Go API, passed to the loader over "
                    "stdin, and retained only in backend memory while row monitoring runs."
                ),
            )
        else:
            st.info(
                "The backend will resolve the configured OCI Vault secret before "
                "starting, then use it only for live row-count monitoring."
            )
        confirmed = st.checkbox(
            "Execute the validated loader job",
            help="The job can download OCI reports and insert committed rows into Oracle.",
        )
        submitted = st.form_submit_button("Start loader execution", type="primary")

    if submitted:
        if not confirmed:
            st.error("Select the execution confirmation checkbox first.")
            return
        try:
            payload = build_loader_execution_payload(config, runtime_password)
            job = FocusAPIClient(api_url, timeout_seconds=60.0).start_loader_job(payload)
        except (CommandValidationError, FocusAPIError) as error:
            st.error(str(error))
            return
        st.session_state["loader_job_id"] = job.job_id
        st.session_state["loader_job_last"] = job
        st.rerun()


def render_cost_analytics_snapshot(job: LoaderJob) -> None:
    month_column, service_column, row_column = st.columns(3)
    month_column.metric("Month/currency rows", len(job.monthly_costs))
    service_column.metric("Unique services", len(job.services))
    row_column.metric(
        "Loaded rows",
        f"{job.current_row_count:,}" if job.current_row_count_known else "Waiting...",
    )

    if job.analytics_updated_at_utc:
        st.caption(
            "Analytics last refreshed at "
            f"{job.analytics_updated_at_utc}. The backend refreshes the snapshot "
            "every 30 seconds while the loader runs."
        )
    if job.analytics_error:
        st.warning(
            "The loader continues running, but the latest cost analytics query failed: "
            f"{job.analytics_error}"
        )

    st.markdown("#### Monthly effective cost")
    st.caption(
        "Totals use CHARGE_PERIOD_START and EFFECTIVE_COST across all loaded tenancy "
        "rows. Billing currencies are kept separate to avoid adding unlike currencies."
    )
    if job.monthly_costs:
        st.dataframe(
            [
                {
                    "Month": cost.month,
                    "Billing currency": cost.billing_currency,
                    "Total effective cost": cost.effective_cost,
                }
                for cost in job.monthly_costs
            ],
            hide_index=True,
            use_container_width=True,
        )
    elif not job.analytics_error:
        st.info("No rows with CHARGE_PERIOD_START are currently available.")

    st.markdown("#### Unique services")
    if job.services:
        st.dataframe(
            [
                {"Position": index, "Service name": service}
                for index, service in enumerate(job.services, start=1)
            ],
            hide_index=True,
            use_container_width=True,
        )
    elif not job.analytics_error:
        st.info("No non-empty SERVICE_NAME values are currently available.")


def render_cost_analytics(api_url: str) -> None:
    st.subheader("Tenancy cost analytics")
    st.caption(
        "Shows a read-only snapshot from TEMP_OCI_FOCUS for the current or most "
        "recent GUI-started loader job. No SQL, table name, or credential is accepted "
        "from this tab."
    )

    active_job_id = st.session_state.get("loader_job_id")
    if isinstance(active_job_id, str) and active_job_id:

        @st.fragment(run_every="10s")
        def poll_cost_analytics() -> None:
            try:
                job = FocusAPIClient(api_url, timeout_seconds=15.0).loader_job(
                    active_job_id
                )
            except FocusAPIError as error:
                st.error(str(error))
                return
            st.session_state["loader_job_last"] = job
            render_cost_analytics_snapshot(job)

        poll_cost_analytics()
        return

    last_job = st.session_state.get("loader_job_last")
    if isinstance(last_job, LoaderJob):
        render_cost_analytics_snapshot(last_job)
        return

    st.info(
        "Start a loader job in the Execute loader tab. Monthly costs and unique "
        "services will appear here and refresh as committed rows become visible."
    )


def render_diagnostics(
    api_url: str,
    health: Health,
    catalog: AliasCatalog,
    execution_user: str,
    executable: str,
) -> None:
    st.subheader("Diagnostics")
    st.json(
        {
            "executionUser": execution_user,
            "configuredExecutable": executable,
            "apiUrl": api_url,
            "backendStatus": health.status,
            "backendVersion": health.version,
            "aliasCount": len(catalog.aliases),
            "firstAlias": catalog.first_alias,
            "sourcePath": catalog.source_path,
            "readAtUtc": catalog.read_at_utc,
        }
    )
    st.code(
        f"curl -fsS {api_url.rstrip('/')}/api/v1/health\n"
        f"curl -fsS {api_url.rstrip('/')}/api/v1/tns/aliases",
        language="bash",
    )


def main() -> None:
    st.title("OCI FOCUS Loader")
    st.caption("Modular Streamlit frontend with a Go control boundary")

    with st.sidebar:
        st.header("Execution identity")
        execution_user = st.selectbox(
            "Run loader and database actions as",
            options=list(EXECUTION_BACKENDS),
            index=0,
            key="execution_user",
            on_change=clear_execution_user_context,
            help=(
                "Selects an isolated loopback Go backend running as this Linux user."
            ),
        )
        backend = EXECUTION_BACKENDS[execution_user]
        api_url = backend["api_url"]
        default_executable = backend["executable"]
        st.text_input(
            "API URL",
            value=api_url,
            disabled=True,
            help="Configured by systemd; browser users cannot override it.",
        )
        st.text_input(
            "Backend executable",
            value=default_executable,
            disabled=True,
            help="The selected backend permits only this server-configured binary.",
        )
        if st.button("Refresh backend", use_container_width=True):
            st.rerun()
        if st.button(
            "Reset interface",
            use_container_width=True,
            help=(
                "Clears all UI forms and results. It does not stop a loader job "
                "that is already running in the backend."
            ),
        ):
            st.session_state.clear()
            st.rerun()
        st.divider()
        st.caption(
            "All services must remain on 127.0.0.1. Enter database credentials "
            "only through the protected SSH/OCI Bastion tunnel."
        )

    health, catalog, error = read_backend(api_url)
    if error or health is None or catalog is None:
        st.error(error or "The Go backend did not return connection information.")
        st.info(
            "Start the Go process with: focus-loader-report-upload "
            "-tns-gui -tns-gui-listen 127.0.0.1:8080"
        )
        st.stop()

    st.caption(
        f"Installed release: {health.version} · "
        f"[GitHub releases]({GITHUB_RELEASES_URL})"
    )
    with st.sidebar:
        st.divider()
        st.markdown(f"**Installed release:** `{health.version}`")
        st.link_button(
            "Open GitHub releases",
            GITHUB_RELEASES_URL,
            use_container_width=True,
        )

    show_deployment_tab = deployment_authorization_is_active() or isinstance(
        st.session_state.get("schema_deployment_result"), SchemaDeployment
    )
    tab_labels = ["Database tables"]
    if show_deployment_tab:
        tab_labels.append("Deploy schema")
    tab_labels.extend(
        [
            "Connection",
            "Command builder",
            "Execute loader",
            "Schema stats",
            "Cost analytics",
            "Diagnostics",
        ]
    )
    tabs = st.tabs(tab_labels)

    tab_index = 0
    with tabs[tab_index]:
        render_database_tables(api_url, catalog)
    tab_index += 1
    if show_deployment_tab:
        with tabs[tab_index]:
            render_schema_deployment(api_url, catalog)
        tab_index += 1
    with tabs[tab_index]:
        selected_alias = render_connection(health, catalog)
    tab_index += 1
    with tabs[tab_index]:
        render_command_builder(selected_alias, default_executable, execution_user)
    tab_index += 1
    with tabs[tab_index]:
        render_loader_execution(api_url)
    tab_index += 1
    with tabs[tab_index]:
        render_schema_statistics(api_url, catalog)
    tab_index += 1
    with tabs[tab_index]:
        render_cost_analytics(api_url)
    tab_index += 1
    with tabs[tab_index]:
        render_diagnostics(
            api_url, health, catalog, execution_user, default_executable
        )


if __name__ == "__main__":
    main()
