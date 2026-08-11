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
                    payload = {"status": "ok", "version": "26.11.0-schema-stats-reset"}
                elif self.path == "/api/v1/tns/aliases":
                    payload = {
                        "aliases": ["FOCUS_HIGH", "FOCUS_LOW"],
                        "firstAlias": "FOCUS_HIGH",
                        "sourcePath": "/opt/oracle/wallet/tnsnames.ora",
                        "readAtUtc": "2026-08-10T12:00:00Z",
                    }
                elif self.path == "/api/v1/loader/jobs/analytics-job":
                    payload = {
                        "jobId": "analytics-job",
                        "status": "running",
                        "databaseUser": "FOCUS_APP",
                        "databaseAlias": "FOCUS_HIGH",
                        "tableName": "TEMP_OCI_FOCUS",
                        "startedAtUtc": "2026-08-10T12:00:00Z",
                        "finishedAtUtc": "",
                        "exitCode": None,
                        "initialRowCount": 100,
                        "initialRowCountKnown": True,
                        "currentRowCount": 125,
                        "currentRowCountKnown": True,
                        "rowsInserted": 25,
                        "rowsInsertedKnown": True,
                        "rowCountUpdatedAtUtc": "2026-08-10T12:00:05Z",
                        "rowCountError": "",
                        "monthlyCosts": [
                            {
                                "month": "2026-01",
                                "billingCurrency": "USD",
                                "effectiveCost": "123.45",
                            }
                        ],
                        "services": ["Compute", "Object Storage"],
                        "analyticsUpdatedAtUtc": "2026-08-10T12:00:05Z",
                        "analyticsError": "",
                        "output": "",
                        "error": "",
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

            def do_POST(self):
                if self.path != "/api/v1/schema/stats":
                    self.send_error(404)
                    return
                content_length = int(self.headers.get("Content-Length", "0"))
                request_payload = json.loads(self.rfile.read(content_length))
                if request_payload.get("password") != "stats-secret":
                    self.send_error(401)
                    return
                payload = {
                    "connectAlias": "FOCUS_HIGH",
                    "username": request_payload["username"],
                    "schema": request_payload["schema"],
                    "tableName": "TEMP_OCI_FOCUS",
                    "tableExists": True,
                    "hasData": True,
                    "totalRows": 125,
                    "lastLoadDate": "2026-08-11T18:30:00",
                    "currentMonth": "2026-08",
                    "currentMonthCosts": [
                        {
                            "month": "2026-08",
                            "billingCurrency": "EUR",
                            "effectiveCost": "42.75",
                        }
                    ],
                    "queriedAtUtc": "2026-08-11T18:31:00Z",
                }
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
            with patch.dict(
                os.environ,
                {
                    "FOCUS_API_URL": api_url,
                    "FOCUS_API_URL_ORACLE": api_url,
                },
            ):
                app = AppTest.from_file(str(app_path), default_timeout=10).run()
                self.assertEqual(len(app.exception), 0)
                execution_selector = next(
                    item
                    for item in app.selectbox
                    if item.label == "Run loader and database actions as"
                )
                self.assertEqual(execution_selector.value, "focusloader")
                self.assertGreaterEqual(len(app.tabs), 5)
                self.assertEqual(app.tabs[0].label, "Database tables")
                self.assertNotIn("Deploy schema", [tab.label for tab in app.tabs])
                self.assertIn("Execute loader", [tab.label for tab in app.tabs])
                self.assertIn("Schema stats", [tab.label for tab in app.tabs])
                self.assertIn("Cost analytics", [tab.label for tab in app.tabs])
                self.assertIn("Reset interface", [button.label for button in app.button])
                requested_defaults = {
                    "Pre-load report (-preload-report)": True,
                    "Continue after report (-continue-after-report)": True,
                    "Skip pre-load content scan (-skip-preload-content-scan)": True,
                    "Skip tag rows (-skip-tag-rows)": True,
                }
                checkbox_values = {
                    checkbox.label: checkbox.value for checkbox in app.checkbox
                }
                for label, expected in requested_defaults.items():
                    self.assertEqual(checkbox_values.get(label), expected)

                stats_login_schema = next(
                    checkbox
                    for checkbox in app.checkbox
                    if checkbox.label == "Check the login user's schema"
                )
                stats_login_schema.uncheck().run()
                next(
                    item
                    for item in app.text_input
                    if item.label == "Statistics schema owner"
                ).input("FOCUS_APP")
                next(
                    item
                    for item in app.text_input
                    if item.label == "Statistics database password"
                ).input("stats-secret")
                next(
                    button
                    for button in app.button
                    if button.label == "Check schema statistics"
                ).click().run()
                self.assertEqual(len(app.exception), 0)
                metric_values = {metric.label: metric.value for metric in app.metric}
                self.assertEqual(metric_values.get("TEMP_OCI_FOCUS exists"), "Yes")
                self.assertEqual(metric_values.get("Table has data"), "Yes")
                self.assertEqual(metric_values.get("Total rows"), "125")
                self.assertEqual(
                    metric_values.get("Last LOAD_DATE"), "2026-08-11T18:30:00"
                )

                app.session_state["database_deployment_token"] = "opaque-test-token"
                app.session_state["database_deployment_token_expires_at_utc"] = (
                    "2999-08-10T12:15:00Z"
                )
                app.session_state["database_admin_user"] = "ADMIN"
                app.run()
                self.assertEqual(len(app.exception), 0)
                self.assertGreaterEqual(len(app.tabs), 6)
                self.assertEqual(app.tabs[1].label, "Deploy schema")
                self.assertIn(
                    "Drop the existing target schema and all its objects",
                    [checkbox.label for checkbox in app.checkbox],
                )
                self.assertNotIn(
                    "Destructive-action confirmation",
                    [text_input.label for text_input in app.text_input],
                )

                app.session_state["loader_job_id"] = "analytics-job"
                app.run()
                self.assertEqual(len(app.exception), 0)
                metric_values = {metric.label: metric.value for metric in app.metric}
                self.assertEqual(metric_values.get("Unique services"), "2")
                self.assertEqual(metric_values.get("Month/currency rows"), "1")
                self.assertIn(
                    "Tenancy cost analytics",
                    [subheader.value for subheader in app.subheader],
                )

                execution_selector.select("oracle").run()
                self.assertEqual(len(app.exception), 0)
                self.assertNotIn("database_deployment_token", app.session_state)
                backend_executable = next(
                    item
                    for item in app.text_input
                    if item.label == "Backend executable"
                )
                self.assertEqual(
                    backend_executable.value,
                    "/home/oracle/focus-loader-report-upload/dist/"
                    "focus-loader-report-upload-linux-amd64",
                )

                app.session_state["schema_stats_error"] = "temporary error"
                reset_button = next(
                    button for button in app.button if button.label == "Reset interface"
                )
                reset_button.click().run()
                self.assertEqual(len(app.exception), 0)
                self.assertNotIn("schema_stats_error", app.session_state)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)


if __name__ == "__main__":
    unittest.main()
