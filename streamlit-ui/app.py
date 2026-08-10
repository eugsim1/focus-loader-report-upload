"""Modular Streamlit frontend for the OCI FOCUS Loader."""

from __future__ import annotations

import csv
import io
import os

import streamlit as st

from command_builder import (
    CommandValidationError,
    LoaderCommandConfig,
    build_loader_command,
    build_shell_script,
)
from focus_api import AliasCatalog, DatabaseTables, FocusAPIClient, FocusAPIError, Health


DEFAULT_API_URL = os.getenv("FOCUS_API_URL", "http://127.0.0.1:8080")
DEFAULT_EXECUTABLE = os.getenv(
    "FOCUS_LOADER_EXECUTABLE", "/opt/focus-loader/focus-loader-report-upload"
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


def database_tables_csv(result: DatabaseTables) -> str:
    output = io.StringIO(newline="")
    writer = csv.writer(output)
    writer.writerow(["OWNER", "TABLE_NAME"])
    for table in result.tables:
        writer.writerow([table.owner, table.table_name])
    return output.getvalue()


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
            data=database_tables_csv(result),
            file_name=f"{result.schema.lower()}-tables.csv",
            mime="text/csv",
        )
        if st.button("Clear database result"):
            st.session_state.pop("database_tables_result", None)
            st.session_state.pop("database_tables_error", None)
            st.rerun()

    st.caption(
        "The backend opens one connection for this request and closes it after "
        "the metadata query. The password field clears after submission."
    )


def render_command_builder(selected_alias: str) -> None:
    st.subheader("Loader command builder")
    st.warning(
        "This page validates and previews a command; it does not execute it. "
        "Review downloaded scripts before running them on Oracle Linux."
    )

    with st.form("loader-command-builder"):
        general, source, destination, options = st.tabs(
            ["General", "Source", "Destination", "Options"]
        )
        with general:
            mode = st.selectbox(
                "Processing mode",
                ["Pre-load report", "Database load", "Upload only", "Upload and load"],
            )
            auth_mode = st.radio(
                "OCI authentication", ["Instance principal", "OCI config file"], horizontal=True
            )
            executable = st.text_input("Loader executable", value=DEFAULT_EXECUTABLE)
            database_user = st.text_input("Database user", value="FOCUS_APP")
            database_alias = st.text_input("Database alias", value=selected_alias)
            vault_secret_ocid = st.text_input(
                "OCI Vault secret OCID",
                placeholder="ocid1.vaultsecret.oc1..replace_me",
                help="Enter the secret OCID only. Never enter a password in the command builder.",
            )
            oci_config_file = st.text_input("OCI config file", value="/home/focusloader/.oci/config")
            oci_profile = st.text_input("OCI profile", value="DEFAULT")

        with source:
            source_namespace = st.text_input("Source namespace", value="bling")
            source_bucket = st.text_input("Source bucket", placeholder="Optional")
            minimum_date = st.text_input("Minimum date", placeholder="YYYY-MM-DD")
            exact_object = st.text_input(
                "Exact object", placeholder="FOCUS Reports/YYYY/MM/DD/file.csv.gz"
            )
            workers = st.number_input("Workers", min_value=1, max_value=128, value=1, step=1)

        with destination:
            destination_namespace = st.text_input("Destination namespace", placeholder="Optional")
            destination_bucket = st.text_input("Destination bucket")
            destination_prefix = st.text_input("Destination prefix", value="transformed-focus")

        with options:
            force = st.checkbox("Force controlled replay")
            skip_tags = st.checkbox("Skip tag processing")
            keep_work_files = st.checkbox("Keep work files")
            verbose = st.checkbox("Verbose diagnostics")

        submitted = st.form_submit_button("Validate and build command", type="primary")

    if submitted:
        config = LoaderCommandConfig(
            executable=executable.strip(),
            mode=mode,
            auth_mode=auth_mode,
            database_user=database_user.strip(),
            database_alias=database_alias.strip(),
            vault_secret_ocid=vault_secret_ocid.strip(),
            source_namespace=source_namespace.strip(),
            source_bucket=source_bucket.strip(),
            oci_config_file=oci_config_file.strip(),
            oci_profile=oci_profile.strip(),
            minimum_date=minimum_date.strip(),
            workers=int(workers),
            destination_namespace=destination_namespace.strip(),
            destination_bucket=destination_bucket.strip(),
            destination_prefix=destination_prefix.strip(),
            exact_object=exact_object.strip(),
            force=force,
            skip_tags=skip_tags,
            keep_work_files=keep_work_files,
            verbose=verbose,
        )
        try:
            st.session_state["loader_command"] = build_loader_command(config)
            st.session_state["loader_script"] = build_shell_script(config)
            st.success("Command validation passed.")
        except CommandValidationError as error:
            st.session_state.pop("loader_command", None)
            st.session_state.pop("loader_script", None)
            st.error(str(error))

    if command := st.session_state.get("loader_command"):
        st.code(command, language="bash", wrap_lines=True)
        st.download_button(
            "Download reviewed command",
            data=st.session_state["loader_script"],
            file_name="run-focus-loader.sh",
            mime="text/x-shellscript",
        )


def render_diagnostics(api_url: str, health: Health, catalog: AliasCatalog) -> None:
    st.subheader("Diagnostics")
    st.json(
        {
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
        st.header("Go backend")
        api_url = DEFAULT_API_URL
        st.text_input(
            "API URL",
            value=api_url,
            disabled=True,
            help="Set FOCUS_API_URL in the systemd environment; browser users cannot override it.",
        )
        if st.button("Refresh backend", use_container_width=True):
            st.rerun()
        st.divider()
        st.caption(
            "Both services must remain on 127.0.0.1. Enter database credentials "
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

    database_tab, connection_tab, builder_tab, diagnostics_tab = st.tabs(
        ["Database tables", "Connection", "Command builder", "Diagnostics"]
    )
    with database_tab:
        render_database_tables(api_url, catalog)
    with connection_tab:
        selected_alias = render_connection(health, catalog)
    with builder_tab:
        render_command_builder(selected_alias)
    with diagnostics_tab:
        render_diagnostics(api_url, health, catalog)


if __name__ == "__main__":
    main()
