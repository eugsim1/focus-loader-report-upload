"""Small, dependency-free client for the local FOCUS Loader Go API."""

from __future__ import annotations

import json
from dataclasses import dataclass
from typing import Any, Callable, Mapping
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit
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
    output: str
    error: str
    tables: tuple[DatabaseTable, ...]


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
            output=output,
            error=error,
            tables=tuple(tables),
        )

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
