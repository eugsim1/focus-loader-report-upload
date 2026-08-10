import io
import json
import unittest
from urllib.error import URLError

from focus_api import FocusAPIClient, FocusAPIError


class FakeResponse(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        self.close()
        return False


class RecordingOpener:
    def __init__(self, payload):
        self.payload = payload
        self.urls = []
        self.requests = []

    def __call__(self, request, timeout):
        self.urls.append((request.full_url, timeout))
        self.requests.append(request)
        return FakeResponse(json.dumps(self.payload).encode("utf-8"))


class FocusAPIClientTests(unittest.TestCase):
    def test_health(self):
        opener = RecordingOpener({"status": "ok", "version": "26.6.0-schema-browser"})
        result = FocusAPIClient("http://127.0.0.1:8080/", opener=opener).health()
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.version, "26.6.0-schema-browser")
        self.assertEqual(opener.urls[0][0], "http://127.0.0.1:8080/api/v1/health")

    def test_aliases(self):
        opener = RecordingOpener(
            {
                "aliases": ["FOCUS_HIGH", "FOCUS_LOW"],
                "firstAlias": "FOCUS_HIGH",
                "sourcePath": "/opt/oracle/wallet/tnsnames.ora",
                "readAtUtc": "2026-08-10T12:00:00Z",
            }
        )
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).aliases()
        self.assertEqual(result.aliases, ("FOCUS_HIGH", "FOCUS_LOW"))
        self.assertEqual(result.first_alias, "FOCUS_HIGH")

    def test_rejects_credentials_in_url(self):
        with self.assertRaisesRegex(FocusAPIError, "credentials"):
            FocusAPIClient("http://user:password@127.0.0.1:8080")

    def test_schema_tables_posts_credentials_and_parses_tables(self):
        opener = RecordingOpener(
            {
                "connectAlias": "FOCUS_HIGH",
                "username": "ADMIN",
                "schema": "FOCUS_APP",
                "tableCount": 2,
                "tables": [
                    {"owner": "FOCUS_APP", "tableName": "LOAD_STATUS"},
                    {"owner": "FOCUS_APP", "tableName": "OCI_FOCUS"},
                ],
            }
        )
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).schema_tables(
            "ADMIN", "test-secret", "FOCUS_APP"
        )
        self.assertEqual(result.connect_alias, "FOCUS_HIGH")
        self.assertEqual([table.table_name for table in result.tables], ["LOAD_STATUS", "OCI_FOCUS"])
        request = opener.requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(request.headers["Content-type"], "application/json")
        self.assertEqual(
            json.loads(request.data.decode("utf-8")),
            {"username": "ADMIN", "password": "test-secret", "schema": "FOCUS_APP"},
        )
        self.assertNotIn("test-secret", request.full_url)

    def test_connection_error_is_safe(self):
        def failing_opener(request, timeout):
            raise URLError("connection refused")

        with self.assertRaisesRegex(FocusAPIError, "Cannot reach"):
            FocusAPIClient("http://127.0.0.1:8080", opener=failing_opener).health()


if __name__ == "__main__":
    unittest.main()
