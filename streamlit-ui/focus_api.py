"""Small, dependency-free client for the local FOCUS Loader Go API."""

from __future__ import annotations

import json
from dataclasses import dataclass
from decimal import Decimal, InvalidOperation
from typing import Any, Callable, Mapping
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlsplit
from urllib.request import Request, urlopen


class FocusAPIError(RuntimeError):
    """A safe, user-facing error returned by the local Go API client."""


@dataclass(frozen=True)
class Health:
    status: str
    version: str


@dataclass(frozen=True)
class AliasCatalog:
    aliases: tuple[str, ...]
    first_alias: str
    source_path: str
    read_at_utc: str


@dataclass(frozen=True)
class DatabaseTable:
    owner: str
    table_name: str


@dataclass(frozen=True)
class DatabaseTables:
    connect_alias: str
    username: str
    schema: str
    tables: tuple[DatabaseTable, ...]
    deployment_token: str
    deployment_token_expires_at_utc: str


@dataclass(frozen=True)
class SchemaDeployment:
    connect_alias: str
    admin_username: str
    schema: str
    script_name: str
    drop_existing: bool
    started_at_utc: str
    finished_at_utc: str
    exit_code: int
    deployment_succeeded: bool
    table_lookup_succeeded: bool
    working_directory: str
    command_line: str
    output: str
    output_truncated: bool
    error: str
    tables: tuple[DatabaseTable, ...]


@dataclass(frozen=True)
class MonthlyEffectiveCost:
    month: str
    billing_currency: str
    effective_cost: str


@dataclass(frozen=True)
class SchemaStatistics:
    connect_alias: str
    username: str
    schema: str
    table_name: str
    table_exists: bool
    has_data: bool
    total_rows: int
    last_load_date: str
    current_month: str
    current_month_costs: tuple[MonthlyEffectiveCost, ...]
    queried_at_utc: str


@dataclass(frozen=True)
class LoaderJob:
    job_id: str
    status: str
    database_user: str
    database_alias: str
    table_name: str
    started_at_utc: str
    finished_at_utc: str
    exit_code: int | None
    initial_row_count: int
    initial_row_count_known: bool
    current_row_count: int
    current_row_count_known: bool
    rows_inserted: int
    rows_inserted_known: bool
    row_count_updated_at_utc: str
    row_count_error: str
    monthly_costs: tuple[MonthlyEffectiveCost, ...]
    services: tuple[str, ...]
    analytics_updated_at_utc: str
    analytics_error: str
    executable: str
    working_directory: str
    command_line: str
    manual_command: str
    tns_admin: str
    home_directory: str
    path_environment: str
    output: str
    output_truncated: bool
    error: str

    @property
    def terminal(self) -> bool:
        return self.status in {"succeeded", "failed", "timed_out"}


class FocusAPIClient:
    def __init__(
        self,
        base_url: str,
        timeout_seconds: float = 5.0,
        opener: Callable[..., Any] = urlopen,
    ) -> None:
        self.base_url = self._normalize_base_url(base_url)
        self.timeout_seconds = timeout_seconds
        self._opener = opener

    @staticmethod
    def _normalize_base_url(value: str) -> str:
        value = value.strip().rstrip("/")
        parsed = urlsplit(value)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise FocusAPIError("The Go API URL must be an http:// or https:// URL.")
        if parsed.username or parsed.password:
            raise FocusAPIError("Do not put credentials in the Go API URL.")
        if parsed.query or parsed.fragment:
            raise FocusAPIError("The Go API URL cannot contain a query or fragment.")
        return value

    def health(self) -> Health:
        payload = self._get_json("/api/v1/health")
        status = self._required_string(payload, "status")
        version = self._required_string(payload, "version")
        return Health(status=status, version=version)

    def aliases(self) -> AliasCatalog:
        payload = self._get_json("/api/v1/tns/aliases")
        raw_aliases = payload.get("aliases")
        if not isinstance(raw_aliases, list) or not all(
            isinstance(alias, str) and alias for alias in raw_aliases
        ):
            raise FocusAPIError("The Go API returned an invalid TNS alias list.")
        if not raw_aliases:
            raise FocusAPIError("The Go API returned no TNS aliases.")

        first_alias = self._required_string(payload, "firstAlias")
        if first_alias not in raw_aliases:
            raise FocusAPIError("The Go API first alias is not present in its alias list.")
        return AliasCatalog(
            aliases=tuple(raw_aliases),
            first_alias=first_alias,
            source_path=self._required_string(payload, "sourcePath"),
            read_at_utc=self._required_string(payload, "readAtUtc"),
        )

    def schema_tables(self, username: str, password: str, schema: str) -> DatabaseTables:
        payload = self._request_json(
            "/api/v1/database/tables",
            method="POST",
            body={"username": username, "password": password, "schema": schema},
            timeout_seconds=max(self.timeout_seconds, 35.0),
        )
        raw_tables = payload.get("tables")
        if not isinstance(raw_tables, list):
            raise FocusAPIError("The Go API returned an invalid database table list.")

        tables: list[DatabaseTable] = []
        for raw_table in raw_tables:
            if not isinstance(raw_table, dict):
                raise FocusAPIError("The Go API returned an invalid database table entry.")
            tables.append(
                DatabaseTable(
                    owner=self._required_string(raw_table, "owner"),
                    table_name=self._required_string(raw_table, "tableName"),
                )
            )
        table_count = payload.get("tableCount")
        if not isinstance(table_count, int) or table_count != len(tables):
            raise FocusAPIError("The Go API returned an inconsistent database table count.")
        return DatabaseTables(
            connect_alias=self._required_string(payload, "connectAlias"),
            username=self._required_string(payload, "username"),
            schema=self._required_string(payload, "schema"),
            tables=tuple(tables),
            deployment_token=self._required_string(payload, "deploymentToken"),
            deployment_token_expires_at_utc=self._required_string(
                payload, "deploymentTokenExpiresAtUtc"
            ),
        )

    def schema_statistics(
        self, username: str, password: str, schema: str
    ) -> SchemaStatistics:
        payload = self._request_json(
            "/api/v1/schema/stats",
            method="POST",
            body={"username": username, "password": password, "schema": schema},
            timeout_seconds=max(self.timeout_seconds, 35.0),
        )
        costs = self._monthly_costs(payload, "currentMonthCosts")
        total_rows = self._required_int(payload, "totalRows")
        table_exists = self._required_bool(payload, "tableExists")
        has_data = self._required_bool(payload, "hasData")
        if total_rows < 0 or has_data != (total_rows > 0):
            raise FocusAPIError("The Go API returned inconsistent schema statistics.")
        if not table_exists and (has_data or total_rows != 0):
            raise FocusAPIError("The Go API returned data for a missing schema table.")
        current_month = self._string(payload, "currentMonth")
        if current_month and (
            len(current_month) != 7
            or current_month[4] != "-"
            or not current_month[:4].isdigit()
            or not current_month[5:].isdigit()
            or not 1 <= int(current_month[5:]) <= 12
        ):
            raise FocusAPIError("The Go API returned an invalid current cost month.")
        return SchemaStatistics(
            connect_alias=self._required_string(payload, "connectAlias"),
            username=self._required_string(payload, "username"),
            schema=self._required_string(payload, "schema"),
            table_name=self._required_string(payload, "tableName"),
            table_exists=table_exists,
            has_data=has_data,
            total_rows=total_rows,
            last_load_date=self._string(payload, "lastLoadDate"),
            current_month=current_month,
            current_month_costs=costs,
            queried_at_utc=self._required_string(payload, "queriedAtUtc"),
        )

    def deploy_schema(
        self,
        deployment_token: str,
        target_schema: str,
        target_schema_password: str,
        drop_existing: bool,
    ) -> SchemaDeployment:
        payload = self._request_json(
            "/api/v1/schema/deploy",
            method="POST",
            body={
                "deploymentToken": deployment_token,
                "targetSchema": target_schema,
                "targetSchemaPassword": target_schema_password,
                "dropExisting": drop_existing,
            },
            timeout_seconds=max(self.timeout_seconds, 630.0),
            raise_api_error=False,
        )
        raw_tables = payload.get("tables")
        if not isinstance(raw_tables, list):
            raise FocusAPIError("The Go API returned an invalid deployed-schema table list.")
        tables: list[DatabaseTable] = []
        for raw_table in raw_tables:
            if not isinstance(raw_table, dict):
                raise FocusAPIError("The Go API returned an invalid deployed-schema table entry.")
            tables.append(
                DatabaseTable(
                    owner=self._required_string(raw_table, "owner"),
                    table_name=self._required_string(raw_table, "tableName"),
                )
            )
        table_count = payload.get("tableCount")
        if not isinstance(table_count, int) or table_count != len(tables):
            raise FocusAPIError("The Go API returned an inconsistent deployed-schema table count.")

        error = payload.get("error", "")
        if not isinstance(error, str):
            raise FocusAPIError("The Go API returned an invalid schema deployment error.")
        output = payload.get("output")
        if not isinstance(output, str):
            raise FocusAPIError("The Go API returned invalid schema deployment output.")

        return SchemaDeployment(
            connect_alias=self._required_string(payload, "connectAlias"),
            admin_username=self._required_string(payload, "adminUsername"),
            schema=self._required_string(payload, "schema"),
            script_name=self._required_string(payload, "scriptName"),
            drop_existing=self._required_bool(payload, "dropExisting"),
            started_at_utc=self._required_string(payload, "startedAtUtc"),
            finished_at_utc=self._required_string(payload, "finishedAtUtc"),
            exit_code=self._required_int(payload, "exitCode"),
            deployment_succeeded=self._required_bool(payload, "deploymentSucceeded"),
            table_lookup_succeeded=self._required_bool(payload, "tableLookupSucceeded"),
            working_directory=self._required_string(payload, "workingDirectory"),
            command_line=self._required_string(payload, "commandLine"),
            output=output,
            output_truncated=self._required_bool(payload, "outputTruncated"),
            error=error,
            tables=tuple(tables),
        )

    def start_loader_job(self, body: Mapping[str, Any]) -> LoaderJob:
        payload = self._request_json(
            "/api/v1/loader/jobs",
            method="POST",
            body=body,
            timeout_seconds=max(self.timeout_seconds, 60.0),
        )
        return self._loader_job(payload)

    def loader_job(self, job_id: str) -> LoaderJob:
        if not job_id or len(job_id) > 512 or any(
            character not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
            for character in job_id
        ):
            raise FocusAPIError("The loader job id is invalid.")
        payload = self._get_json("/api/v1/loader/jobs/" + quote(job_id, safe=""))
        return self._loader_job(payload)

    def _loader_job(self, payload: Mapping[str, Any]) -> LoaderJob:
        status = self._required_string(payload, "status")
        if status not in {"starting", "running", "succeeded", "failed", "timed_out"}:
            raise FocusAPIError("The Go API returned an invalid loader job status.")
        exit_code = payload.get("exitCode")
        if exit_code is not None and (
            not isinstance(exit_code, int) or isinstance(exit_code, bool)
        ):
            raise FocusAPIError("The Go API returned an invalid loader exit code.")
        monthly_costs = self._monthly_costs(payload, "monthlyCosts")
        raw_services = payload.get("services", [])
        if raw_services is None:
            raw_services = []
        if not isinstance(raw_services, list) or not all(
            isinstance(service, str) and service for service in raw_services
        ):
            raise FocusAPIError("The Go API returned an invalid unique service list.")
        return LoaderJob(
            job_id=self._required_string(payload, "jobId"),
            status=status,
            database_user=self._required_string(payload, "databaseUser"),
            database_alias=self._required_string(payload, "databaseAlias"),
            table_name=self._required_string(payload, "tableName"),
            started_at_utc=self._string(payload, "startedAtUtc"),
            finished_at_utc=self._string(payload, "finishedAtUtc"),
            exit_code=exit_code,
            initial_row_count=self._required_int(payload, "initialRowCount"),
            initial_row_count_known=self._required_bool(
                payload, "initialRowCountKnown"
            ),
            current_row_count=self._required_int(payload, "currentRowCount"),
            current_row_count_known=self._required_bool(
                payload, "currentRowCountKnown"
            ),
            rows_inserted=self._required_int(payload, "rowsInserted"),
            rows_inserted_known=self._required_bool(payload, "rowsInsertedKnown"),
            row_count_updated_at_utc=self._string(payload, "rowCountUpdatedAtUtc"),
            row_count_error=self._string(payload, "rowCountError"),
            monthly_costs=monthly_costs,
            services=tuple(raw_services),
            analytics_updated_at_utc=self._optional_string(
                payload, "analyticsUpdatedAtUtc"
            ),
            analytics_error=self._optional_string(payload, "analyticsError"),
            executable=self._optional_string(payload, "executable"),
            working_directory=self._optional_string(payload, "workingDirectory"),
            command_line=self._optional_string(payload, "commandLine"),
            manual_command=self._optional_string(payload, "manualCommand"),
            tns_admin=self._optional_string(payload, "tnsAdmin"),
            home_directory=self._optional_string(payload, "homeDirectory"),
            path_environment=self._optional_string(payload, "pathEnvironment"),
            output=self._string(payload, "output"),
            output_truncated=self._optional_bool(payload, "outputTruncated"),
            error=self._string(payload, "error"),
        )

    def _monthly_costs(
        self, payload: Mapping[str, Any], field_name: str
    ) -> tuple[MonthlyEffectiveCost, ...]:
        raw_monthly_costs = payload.get(field_name, [])
        if raw_monthly_costs is None:
            raw_monthly_costs = []
        if not isinstance(raw_monthly_costs, list):
            raise FocusAPIError("The Go API returned an invalid monthly cost list.")
        monthly_costs: list[MonthlyEffectiveCost] = []
        for raw_cost in raw_monthly_costs:
            if not isinstance(raw_cost, dict):
                raise FocusAPIError("The Go API returned an invalid monthly cost entry.")
            month = self._required_string(raw_cost, "month")
            if (
                len(month) != 7
                or month[4] != "-"
                or not month[:4].isdigit()
                or not month[5:].isdigit()
                or not 1 <= int(month[5:]) <= 12
            ):
                raise FocusAPIError("The Go API returned an invalid monthly cost month.")
            effective_cost = self._required_string(raw_cost, "effectiveCost")
            try:
                parsed_cost = Decimal(effective_cost)
            except InvalidOperation as error:
                raise FocusAPIError(
                    "The Go API returned an invalid monthly effective cost."
                ) from error
            if not parsed_cost.is_finite():
                raise FocusAPIError("The Go API returned a non-finite monthly effective cost.")
            monthly_costs.append(
                MonthlyEffectiveCost(
                    month=month,
                    billing_currency=self._required_string(
                        raw_cost, "billingCurrency"
                    ),
                    effective_cost=effective_cost,
                )
            )
        return tuple(monthly_costs)

    def _get_json(self, path: str) -> Mapping[str, Any]:
        return self._request_json(path, method="GET")

    def _request_json(
        self,
        path: str,
        method: str,
        body: Mapping[str, Any] | None = None,
        timeout_seconds: float | None = None,
        raise_api_error: bool = True,
    ) -> Mapping[str, Any]:
        encoded_body = None
        headers = {"Accept": "application/json", "User-Agent": "focus-loader-streamlit-ui"}
        if body is not None:
            encoded_body = json.dumps(body, separators=(",", ":")).encode("utf-8")
            headers["Content-Type"] = "application/json"
        request = Request(
            self.base_url + path,
            data=encoded_body,
            method=method,
            headers=headers,
        )
        try:
            with self._opener(
                request,
                timeout=timeout_seconds if timeout_seconds is not None else self.timeout_seconds,
            ) as response:
                raw = response.read()
        except HTTPError as error:
            detail = self._http_error_detail(error)
            raise FocusAPIError(f"Go API returned HTTP {error.code}: {detail}") from error
        except URLError as error:
            reason = getattr(error, "reason", error)
            raise FocusAPIError(f"Cannot reach the Go API: {reason}") from error
        except TimeoutError as error:
            raise FocusAPIError("The Go API request timed out.") from error

        try:
            payload = json.loads(raw.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as error:
            raise FocusAPIError("The Go API returned invalid JSON.") from error
        if not isinstance(payload, dict):
            raise FocusAPIError("The Go API returned an unexpected JSON value.")
        if raise_api_error and isinstance(payload.get("error"), str) and payload["error"]:
            raise FocusAPIError(payload["error"])
        return payload

    @staticmethod
    def _http_error_detail(error: HTTPError) -> str:
        try:
            payload = json.loads(error.read().decode("utf-8"))
            if isinstance(payload, dict) and isinstance(payload.get("error"), str):
                return payload["error"]
        except (UnicodeDecodeError, json.JSONDecodeError):
            pass
        return error.reason or "request failed"

    @staticmethod
    def _required_string(payload: Mapping[str, Any], name: str) -> str:
        value = payload.get(name)
        if not isinstance(value, str) or not value:
            raise FocusAPIError(f"The Go API response is missing {name}.")
        return value

    @staticmethod
    def _string(payload: Mapping[str, Any], name: str) -> str:
        value = payload.get(name)
        if not isinstance(value, str):
            raise FocusAPIError(f"The Go API response is missing {name}.")
        return value

    @staticmethod
    def _optional_string(payload: Mapping[str, Any], name: str) -> str:
        value = payload.get(name, "")
        if value is None:
            return ""
        if not isinstance(value, str):
            raise FocusAPIError(f"The Go API returned an invalid {name}.")
        return value

    @staticmethod
    def _optional_bool(payload: Mapping[str, Any], name: str) -> bool:
        value = payload.get(name, False)
        if not isinstance(value, bool):
            raise FocusAPIError(f"The Go API returned an invalid {name}.")
        return value

    @staticmethod
    def _required_bool(payload: Mapping[str, Any], name: str) -> bool:
        value = payload.get(name)
        if not isinstance(value, bool):
            raise FocusAPIError(f"The Go API response is missing {name}.")
        return value

    @staticmethod
    def _required_int(payload: Mapping[str, Any], name: str) -> int:
        value = payload.get(name)
        if not isinstance(value, int) or isinstance(value, bool):
            raise FocusAPIError(f"The Go API response is missing {name}.")
        return value
