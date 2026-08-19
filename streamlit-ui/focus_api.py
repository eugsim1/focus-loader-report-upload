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
class YearToDateCost:
    billing_currency: str
    effective_cost: str


@dataclass(frozen=True)
class FinopsAnomaly:
    dimension_type: str
    dimension_value: str
    month: str
    billing_currency: str
    effective_cost: str
    anomaly_probability: str
    prediction: int


@dataclass(frozen=True)
class FinopsForecast:
    created_by: str
    billing_currency: str
    month: str
    horizon_months: int
    prediction: str
    lower_bound: str
    upper_bound: str


@dataclass(frozen=True)
class FinopsOMLStatus:
    installed: bool
    refreshed: bool
    last_run_at_utc: str
    status: str
    message: str
    models: tuple[str, ...]


@dataclass(frozen=True)
class FinopsAnalytics:
    connect_alias: str
    username: str
    schema: str
    table_name: str
    table_exists: bool
    last_loaded_date: str
    last_charge_date: str
    year_to_date_start: str
    monthly_costs: tuple[MonthlyEffectiveCost, ...]
    year_to_date_costs: tuple[YearToDateCost, ...]
    anomalies: tuple[FinopsAnomaly, ...]
    forecasts: tuple[FinopsForecast, ...]
    most_expensive_user: str
    most_expensive_cost: str
    most_expensive_currency: str
    oml: FinopsOMLStatus
    queried_at_utc: str


@dataclass(frozen=True)
class LoaderJob:
    job_id: str
    status: str
    database_user: str
    database_alias: str
    table_name: str
    created_at_utc: str
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
    work_report_directory: str
    work_report_reset_at_utc: str
    last_files_loaded: tuple[str, ...]
    output: str
    output_truncated: bool
    error: str

    @property
    def terminal(self) -> bool:
        return self.status in {"succeeded", "failed", "timed_out", "interrupted"}


@dataclass(frozen=True)
class LoaderJobHistoryEntry:
    job_id: str
    created_at_utc: str
    started_at_utc: str
    finished_at_utc: str
    database_user: str
    database_alias: str
    status: str
    rows_inserted: int
    rows_inserted_known: bool
    current_row_count: int
    current_row_count_known: bool
    last_files_loaded: tuple[str, ...]


@dataclass(frozen=True)
class LoaderJobHistory:
    active_job_id: str
    jobs: tuple[LoaderJobHistoryEntry, ...]


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

    def finops_analytics(
        self,
        username: str,
        password: str,
        schema: str,
        *,
        refresh_oml: bool = False,
        confirm_oml_refresh: bool = False,
        outlier_rate: float = 0.05,
    ) -> FinopsAnalytics:
        payload = self._request_json(
            "/api/v1/analytics/finops",
            method="POST",
            body={
                "username": username,
                "password": password,
                "schema": schema,
                "refreshOml": refresh_oml,
                "confirmOmlRefresh": confirm_oml_refresh,
                "outlierRate": outlier_rate,
            },
            timeout_seconds=max(self.timeout_seconds, 310.0),
        )
        monthly_costs = self._monthly_costs(payload, "monthlyCosts")
        year_to_date_costs = self._year_to_date_costs(payload)
        anomalies = self._finops_anomalies(payload)
        forecasts = self._finops_forecasts(payload)
        raw_oml = payload.get("oml")
        if not isinstance(raw_oml, dict):
            raise FocusAPIError("The Go API returned an invalid OML status.")
        oml = FinopsOMLStatus(
            installed=self._required_bool(raw_oml, "installed"),
            refreshed=self._required_bool(raw_oml, "refreshed"),
            last_run_at_utc=self._string(raw_oml, "lastRunAtUtc"),
            status=self._string(raw_oml, "status"),
            message=self._string(raw_oml, "message"),
            models=self._string_tuple(raw_oml, "models"),
        )
        most_expensive_user = self._string(payload, "mostExpensiveUser")
        most_expensive_cost = self._string(payload, "mostExpensiveCost")
        most_expensive_currency = self._string(
            payload, "mostExpensiveCurrency"
        )
        if most_expensive_cost:
            self._finite_decimal(most_expensive_cost, "most-expensive-user cost")
        if forecasts:
            if not (
                most_expensive_user
                and most_expensive_cost
                and most_expensive_currency
            ):
                raise FocusAPIError(
                    "The Go API omitted the most-expensive-user forecast context."
                )
            if any(
                forecast.created_by != most_expensive_user
                or forecast.billing_currency != most_expensive_currency
                for forecast in forecasts
            ):
                raise FocusAPIError(
                    "The Go API returned inconsistent forecast user/currency context."
                )
        return FinopsAnalytics(
            connect_alias=self._required_string(payload, "connectAlias"),
            username=self._required_string(payload, "username"),
            schema=self._required_string(payload, "schema"),
            table_name=self._required_string(payload, "tableName"),
            table_exists=self._required_bool(payload, "tableExists"),
            last_loaded_date=self._string(payload, "lastLoadedDate"),
            last_charge_date=self._string(payload, "lastChargeDate"),
            year_to_date_start=self._string(payload, "yearToDateStart"),
            monthly_costs=monthly_costs,
            year_to_date_costs=year_to_date_costs,
            anomalies=anomalies,
            forecasts=forecasts,
            most_expensive_user=most_expensive_user,
            most_expensive_cost=most_expensive_cost,
            most_expensive_currency=most_expensive_currency,
            oml=oml,
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

    def loader_job_history(self) -> LoaderJobHistory:
        payload = self._get_json("/api/v1/loader/jobs")
        active_job_id = self._optional_string(payload, "activeJobId")
        raw_jobs = payload.get("jobs")
        if not isinstance(raw_jobs, list):
            raise FocusAPIError("The Go API returned an invalid loader job history.")
        jobs: list[LoaderJobHistoryEntry] = []
        for raw_job in raw_jobs:
            if not isinstance(raw_job, dict):
                raise FocusAPIError("The Go API returned an invalid loader history entry.")
            status = self._loader_status(raw_job)
            jobs.append(
                LoaderJobHistoryEntry(
                    job_id=self._required_string(raw_job, "jobId"),
                    created_at_utc=self._optional_string(raw_job, "createdAtUtc"),
                    started_at_utc=self._optional_string(raw_job, "startedAtUtc"),
                    finished_at_utc=self._optional_string(raw_job, "finishedAtUtc"),
                    database_user=self._required_string(raw_job, "databaseUser"),
                    database_alias=self._required_string(raw_job, "databaseAlias"),
                    status=status,
                    rows_inserted=self._required_int(raw_job, "rowsInserted"),
                    rows_inserted_known=self._required_bool(
                        raw_job, "rowsInsertedKnown"
                    ),
                    current_row_count=self._required_int(raw_job, "currentRowCount"),
                    current_row_count_known=self._required_bool(
                        raw_job, "currentRowCountKnown"
                    ),
                    last_files_loaded=self._string_tuple(
                        raw_job, "lastFilesLoaded"
                    ),
                )
            )
        if active_job_id and active_job_id not in {job.job_id for job in jobs}:
            raise FocusAPIError("The active loader job is missing from its history.")
        return LoaderJobHistory(active_job_id=active_job_id, jobs=tuple(jobs))

    def _loader_job(self, payload: Mapping[str, Any]) -> LoaderJob:
        status = self._loader_status(payload)
        exit_code = payload.get("exitCode")
        if exit_code is not None and (
            not isinstance(exit_code, int) or isinstance(exit_code, bool)
        ):
            raise FocusAPIError("The Go API returned an invalid loader exit code.")
        monthly_costs = self._monthly_costs(payload, "monthlyCosts")
        raw_services = self._string_tuple(payload, "services")
        return LoaderJob(
            job_id=self._required_string(payload, "jobId"),
            status=status,
            database_user=self._required_string(payload, "databaseUser"),
            database_alias=self._required_string(payload, "databaseAlias"),
            table_name=self._required_string(payload, "tableName"),
            created_at_utc=self._optional_string(payload, "createdAtUtc"),
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
            services=raw_services,
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
            work_report_directory=self._optional_string(
                payload, "workReportDirectory"
            ),
            work_report_reset_at_utc=self._optional_string(
                payload, "workReportResetAtUtc"
            ),
            last_files_loaded=self._string_tuple(payload, "lastFilesLoaded"),
            output=self._string(payload, "output"),
            output_truncated=self._optional_bool(payload, "outputTruncated"),
            error=self._string(payload, "error"),
        )

    def _loader_status(self, payload: Mapping[str, Any]) -> str:
        status = self._required_string(payload, "status")
        if status not in {
            "starting",
            "running",
            "succeeded",
            "failed",
            "timed_out",
            "interrupted",
        }:
            raise FocusAPIError("The Go API returned an invalid loader job status.")
        return status

    def _string_tuple(
        self, payload: Mapping[str, Any], field_name: str
    ) -> tuple[str, ...]:
        raw_values = payload.get(field_name, [])
        if raw_values is None:
            raw_values = []
        if not isinstance(raw_values, list) or not all(
            isinstance(value, str) and value for value in raw_values
        ):
            raise FocusAPIError(f"The Go API returned an invalid {field_name} list.")
        return tuple(raw_values)

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

    def _year_to_date_costs(
        self, payload: Mapping[str, Any]
    ) -> tuple[YearToDateCost, ...]:
        raw_costs = payload.get("yearToDateCosts", [])
        if not isinstance(raw_costs, list):
            raise FocusAPIError("The Go API returned an invalid year-to-date cost list.")
        costs: list[YearToDateCost] = []
        for raw_cost in raw_costs:
            if not isinstance(raw_cost, dict):
                raise FocusAPIError("The Go API returned an invalid year-to-date cost entry.")
            value = self._required_string(raw_cost, "effectiveCost")
            self._finite_decimal(value, "year-to-date effective cost")
            costs.append(
                YearToDateCost(
                    billing_currency=self._required_string(
                        raw_cost, "billingCurrency"
                    ),
                    effective_cost=value,
                )
            )
        return tuple(costs)

    def _finops_anomalies(
        self, payload: Mapping[str, Any]
    ) -> tuple[FinopsAnomaly, ...]:
        raw_anomalies = payload.get("anomalies", [])
        if not isinstance(raw_anomalies, list):
            raise FocusAPIError("The Go API returned an invalid OML anomaly list.")
        anomalies: list[FinopsAnomaly] = []
        for raw_anomaly in raw_anomalies:
            if not isinstance(raw_anomaly, dict):
                raise FocusAPIError("The Go API returned an invalid OML anomaly entry.")
            dimension_type = self._required_string(raw_anomaly, "dimensionType")
            if dimension_type not in {"TOTAL", "SERVICE", "REGION", "CREATEDBY"}:
                raise FocusAPIError("The Go API returned an invalid OML anomaly dimension.")
            month = self._required_month(raw_anomaly, "month")
            effective_cost = self._required_string(raw_anomaly, "effectiveCost")
            probability = self._required_string(raw_anomaly, "anomalyProbability")
            self._finite_decimal(effective_cost, "anomaly effective cost")
            parsed_probability = self._finite_decimal(
                probability, "anomaly probability"
            )
            if parsed_probability < 0 or parsed_probability > 1:
                raise FocusAPIError("The Go API returned an out-of-range anomaly probability.")
            prediction = self._required_int(raw_anomaly, "prediction")
            if prediction not in {0, 1}:
                raise FocusAPIError("The Go API returned an invalid anomaly prediction.")
            anomalies.append(
                FinopsAnomaly(
                    dimension_type=dimension_type,
                    dimension_value=self._required_string(
                        raw_anomaly, "dimensionValue"
                    ),
                    month=month,
                    billing_currency=self._required_string(
                        raw_anomaly, "billingCurrency"
                    ),
                    effective_cost=effective_cost,
                    anomaly_probability=probability,
                    prediction=prediction,
                )
            )
        return tuple(anomalies)

    def _finops_forecasts(
        self, payload: Mapping[str, Any]
    ) -> tuple[FinopsForecast, ...]:
        raw_forecasts = payload.get("forecasts", [])
        if not isinstance(raw_forecasts, list):
            raise FocusAPIError("The Go API returned an invalid OML forecast list.")
        forecasts: list[FinopsForecast] = []
        for raw_forecast in raw_forecasts:
            if not isinstance(raw_forecast, dict):
                raise FocusAPIError("The Go API returned an invalid OML forecast entry.")
            horizon = self._required_int(raw_forecast, "horizonMonths")
            if not 1 <= horizon <= 6:
                raise FocusAPIError("The Go API returned an invalid forecast horizon.")
            prediction = self._required_string(raw_forecast, "prediction")
            lower = self._required_string(raw_forecast, "lowerBound")
            upper = self._required_string(raw_forecast, "upperBound")
            prediction_value = self._finite_decimal(prediction, "forecast prediction")
            lower_value = self._finite_decimal(lower, "forecast lower bound")
            upper_value = self._finite_decimal(upper, "forecast upper bound")
            if lower_value > prediction_value or prediction_value > upper_value:
                raise FocusAPIError("The Go API returned inconsistent forecast bounds.")
            forecasts.append(
                FinopsForecast(
                    created_by=self._required_string(raw_forecast, "createdBy"),
                    billing_currency=self._required_string(
                        raw_forecast, "billingCurrency"
                    ),
                    month=self._required_month(raw_forecast, "month"),
                    horizon_months=horizon,
                    prediction=prediction,
                    lower_bound=lower,
                    upper_bound=upper,
                )
            )
        if [forecast.horizon_months for forecast in forecasts] != sorted(
            forecast.horizon_months for forecast in forecasts
        ):
            raise FocusAPIError("The Go API returned unsorted forecast horizons.")
        return tuple(forecasts)

    def _required_month(self, payload: Mapping[str, Any], name: str) -> str:
        month = self._required_string(payload, name)
        if (
            len(month) != 7
            or month[4] != "-"
            or not month[:4].isdigit()
            or not month[5:].isdigit()
            or not 1 <= int(month[5:]) <= 12
        ):
            raise FocusAPIError(f"The Go API returned an invalid {name}.")
        return month

    @staticmethod
    def _finite_decimal(value: str, label: str) -> Decimal:
        try:
            parsed = Decimal(value)
        except InvalidOperation as error:
            raise FocusAPIError(f"The Go API returned an invalid {label}.") from error
        if not parsed.is_finite():
            raise FocusAPIError(f"The Go API returned a non-finite {label}.")
        return parsed

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
