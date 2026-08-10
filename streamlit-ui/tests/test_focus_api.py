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

    def __call__(self, request, timeout):
        self.urls.append((request.full_url, timeout))
        return FakeResponse(json.dumps(self.payload).encode("utf-8"))


class FocusAPIClientTests(unittest.TestCase):
    def test_health(self):
        opener = RecordingOpener({"status": "ok", "version": "26.5.4-streamlit"})
        result = FocusAPIClient("http://127.0.0.1:8080/", opener=opener).health()
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.version, "26.5.4-streamlit")
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

    def test_connection_error_is_safe(self):
        def failing_opener(request, timeout):
            raise URLError("connection refused")

        with self.assertRaisesRegex(FocusAPIError, "Cannot reach"):
            FocusAPIClient("http://127.0.0.1:8080", opener=failing_opener).health()


if __name__ == "__main__":
    unittest.main()
