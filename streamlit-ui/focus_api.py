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

    def _get_json(self, path: str) -> Mapping[str, Any]:
        request = Request(
            self.base_url + path,
            method="GET",
            headers={"Accept": "application/json", "User-Agent": "focus-loader-streamlit-ui"},
        )
        try:
            with self._opener(request, timeout=self.timeout_seconds) as response:
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
        if isinstance(payload.get("error"), str) and payload["error"]:
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
