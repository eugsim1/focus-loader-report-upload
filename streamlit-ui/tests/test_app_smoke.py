import json
import os
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

try:
    from streamlit.testing.v1 import AppTest
except ModuleNotFoundError:
    AppTest = None


@unittest.skipIf(AppTest is None, "Streamlit is not installed")
class StreamlitAppSmokeTests(unittest.TestCase):
    def test_app_renders_safe_backend_error(self):
        app_path = Path(__file__).resolve().parents[1] / "app.py"
        app = AppTest.from_file(str(app_path), default_timeout=10).run()
        self.assertEqual(len(app.exception), 0)
        self.assertEqual(app.title[0].value, "OCI FOCUS Loader")
        self.assertGreaterEqual(len(app.error), 1)

    def test_database_tables_is_first_tab_and_deployment_is_initially_locked(self):
        class BackendHandler(BaseHTTPRequestHandler):
            def do_GET(self):
                if self.path == "/api/v1/health":
                    payload = {"status": "ok", "version": "26.8.0-command-builder-flags"}
                elif self.path == "/api/v1/tns/aliases":
                    payload = {
                        "aliases": ["FOCUS_HIGH", "FOCUS_LOW"],
                        "firstAlias": "FOCUS_HIGH",
                        "sourcePath": "/opt/oracle/wallet/tnsnames.ora",
                        "readAtUtc": "2026-08-10T12:00:00Z",
                    }
                else:
                    self.send_error(404)
                    return
                body = json.dumps(payload).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, format, *args):
                return

        server = ThreadingHTTPServer(("127.0.0.1", 0), BackendHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        app_path = Path(__file__).resolve().parents[1] / "app.py"
        api_url = f"http://127.0.0.1:{server.server_port}"
        try:
            with patch.dict(os.environ, {"FOCUS_API_URL": api_url}):
                app = AppTest.from_file(str(app_path), default_timeout=10).run()
                self.assertEqual(len(app.exception), 0)
                self.assertGreaterEqual(len(app.tabs), 4)
                self.assertEqual(app.tabs[0].label, "Database tables")
                self.assertNotIn("Deploy schema", [tab.label for tab in app.tabs])

                app.session_state["database_deployment_token"] = "opaque-test-token"
                app.session_state["database_deployment_token_expires_at_utc"] = (
                    "2999-08-10T12:15:00Z"
                )
                app.session_state["database_admin_user"] = "ADMIN"
                app.run()
                self.assertEqual(len(app.exception), 0)
                self.assertGreaterEqual(len(app.tabs), 5)
                self.assertEqual(app.tabs[1].label, "Deploy schema")
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


if __name__ == "__main__":
    unittest.main()
