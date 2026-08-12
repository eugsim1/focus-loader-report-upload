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
        opener = RecordingOpener(
            {"status": "ok", "version": "26.16.0-persistent-loader-history"}
        )
        result = FocusAPIClient("http://127.0.0.1:8080/", opener=opener).health()
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.version, "26.16.0-persistent-loader-history")
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
                "deploymentToken": "opaque-one-use-token",
                "deploymentTokenExpiresAtUtc": "2026-08-10T12:15:00Z",
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
        self.assertEqual(result.deployment_token, "opaque-one-use-token")
        self.assertEqual([table.table_name for table in result.tables], ["LOAD_STATUS", "OCI_FOCUS"])
        request = opener.requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(request.headers["Content-type"], "application/json")
        self.assertEqual(
            json.loads(request.data.decode("utf-8")),
            {"username": "ADMIN", "password": "test-secret", "schema": "FOCUS_APP"},
        )
        self.assertNotIn("test-secret", request.full_url)

    def test_schema_statistics_posts_credentials_and_parses_fixed_metrics(self):
        opener = RecordingOpener(
            {
                "connectAlias": "FOCUS_HIGH",
                "username": "ADMIN",
                "schema": "FOCUS_APP",
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
        )
        result = FocusAPIClient(
            "http://127.0.0.1:8080", opener=opener
        ).schema_statistics("ADMIN", "test-secret", "FOCUS_APP")
        self.assertTrue(result.table_exists)
        self.assertTrue(result.has_data)
        self.assertEqual(result.total_rows, 125)
        self.assertEqual(result.last_load_date, "2026-08-11T18:30:00")
        self.assertEqual(result.current_month_costs[0].effective_cost, "42.75")
        request = opener.requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(
            request.full_url, "http://127.0.0.1:8080/api/v1/schema/stats"
        )
        self.assertEqual(
            json.loads(request.data.decode("utf-8")),
            {"username": "ADMIN", "password": "test-secret", "schema": "FOCUS_APP"},
        )
        self.assertNotIn("test-secret", request.full_url)

    def test_schema_deployment_posts_token_and_password_and_parses_result(self):
        opener = RecordingOpener(
            {
                "connectAlias": "FOCUS_HIGH",
                "adminUsername": "ADMIN",
                "schema": "FOCUS_APP",
                "scriptName": "run_deploy_focus_schema_with_sqlloader_audit.sh",
                "dropExisting": False,
                "startedAtUtc": "2026-08-10T12:00:00Z",
                "finishedAtUtc": "2026-08-10T12:00:15Z",
                "exitCode": 0,
                "deploymentSucceeded": True,
                "tableLookupSucceeded": True,
                "tableCount": 1,
                "tables": [{"owner": "FOCUS_APP", "tableName": "TEMP_OCI_FOCUS"}],
                "workingDirectory": "/opt/focus-loader/sql_scripts",
                "commandLine": "export DB_ADMIN_PASSWORD='[REDACTED]'\n./run_deploy_focus_schema_with_sqlloader_audit.sh",
                "output": "SCHEMA DEPLOYMENT COMPLETE",
                "outputTruncated": False,
            }
        )
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).deploy_schema(
            "opaque-token", "FOCUS_APP", "target-secret", False
        )
        self.assertTrue(result.deployment_succeeded)
        self.assertEqual(result.tables[0].table_name, "TEMP_OCI_FOCUS")
        self.assertIn("run_deploy_focus_schema", result.command_line)
        self.assertFalse(result.output_truncated)
        request = opener.requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(request.full_url, "http://127.0.0.1:8080/api/v1/schema/deploy")
        self.assertEqual(
            json.loads(request.data.decode("utf-8")),
            {
                "deploymentToken": "opaque-token",
                "targetSchema": "FOCUS_APP",
                "targetSchemaPassword": "target-secret",
                "dropExisting": False,
            },
        )
        self.assertNotIn("target-secret", request.full_url)

    def test_schema_deployment_returns_script_failure_with_output(self):
        opener = RecordingOpener(
            {
                "connectAlias": "FOCUS_HIGH",
                "adminUsername": "ADMIN",
                "schema": "FOCUS_APP",
                "scriptName": "run_deploy_focus_schema_with_sqlloader_audit.sh",
                "dropExisting": True,
                "startedAtUtc": "2026-08-10T12:00:00Z",
                "finishedAtUtc": "2026-08-10T12:00:01Z",
                "exitCode": 1,
                "deploymentSucceeded": False,
                "tableLookupSucceeded": False,
                "tableCount": 0,
                "tables": [],
                "workingDirectory": "/opt/focus-loader/sql_scripts",
                "commandLine": "export DB_ADMIN_PASSWORD='[REDACTED]'\n./run_deploy_focus_schema_with_sqlloader_audit.sh",
                "output": "sqlplus not found in PATH",
                "outputTruncated": False,
                "error": "schema deployment failed: exit status 1",
            }
        )
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).deploy_schema(
            "opaque-token", "FOCUS_APP", "target-secret", True
        )
        self.assertFalse(result.deployment_succeeded)
        self.assertEqual(result.exit_code, 1)
        self.assertIn("sqlplus not found", result.output)
        self.assertIn("deployment failed", result.error)

    def test_start_loader_job_posts_structured_payload_and_parses_live_counts(self):
        opener = RecordingOpener(
            {
                "jobId": "safe-job-token",
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
        )
        request_body = {
            "databaseUser": "FOCUS_APP",
            "databasePassword": "runtime-only",
        }
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).start_loader_job(
            request_body
        )
        self.assertEqual(result.rows_inserted, 25)
        self.assertEqual(result.current_row_count, 125)
        self.assertFalse(result.terminal)
        self.assertEqual(result.monthly_costs[0].effective_cost, "123.45")
        self.assertEqual(result.services, ("Compute", "Object Storage"))
        request = opener.requests[0]
        self.assertEqual(request.method, "POST")
        self.assertEqual(request.full_url, "http://127.0.0.1:8080/api/v1/loader/jobs")
        self.assertEqual(json.loads(request.data.decode("utf-8")), request_body)
        self.assertNotIn("runtime-only", request.full_url)

    def test_loader_job_status_parses_terminal_result(self):
        opener = RecordingOpener(
            {
                "jobId": "safe-job-token",
                "status": "succeeded",
                "databaseUser": "FOCUS_APP",
                "databaseAlias": "FOCUS_HIGH",
                "tableName": "TEMP_OCI_FOCUS",
                "startedAtUtc": "2026-08-10T12:00:00Z",
                "finishedAtUtc": "2026-08-10T12:01:00Z",
                "exitCode": 0,
                "initialRowCount": 100,
                "initialRowCountKnown": True,
                "currentRowCount": 150,
                "currentRowCountKnown": True,
                "rowsInserted": 50,
                "rowsInsertedKnown": True,
                "rowCountUpdatedAtUtc": "2026-08-10T12:01:00Z",
                "rowCountError": "",
                "monthlyCosts": [],
                "services": [],
                "analyticsUpdatedAtUtc": "2026-08-10T12:01:00Z",
                "analyticsError": "",
                "executable": "/opt/focus-loader/focus-loader-report-upload",
                "workingDirectory": "/opt/focus-loader",
                "commandLine": "'/opt/focus-loader/focus-loader-report-upload' '-dp-stdin'",
                "manualCommand": "cd -- '/opt/focus-loader' && secure-command",
                "tnsAdmin": "/home/oracle/adb_wallet",
                "homeDirectory": "/home/oracle",
                "pathEnvironment": "/usr/local/bin:/usr/bin",
                "workReportDirectory": "/opt/focus-loader/work_report_dir",
                "workReportResetAtUtc": "2026-08-10T11:59:59Z",
                "output": "Completed",
                "outputTruncated": False,
                "error": "",
            }
        )
        result = FocusAPIClient("http://127.0.0.1:8080", opener=opener).loader_job(
            "safe-job-token"
        )
        self.assertTrue(result.terminal)
        self.assertEqual(result.exit_code, 0)
        self.assertEqual(result.output, "Completed")
        self.assertIn("-dp-stdin", result.command_line)
        self.assertIn("secure-command", result.manual_command)
        self.assertEqual(result.working_directory, "/opt/focus-loader")
        self.assertEqual(result.tns_admin, "/home/oracle/adb_wallet")
        self.assertEqual(
            result.work_report_directory, "/opt/focus-loader/work_report_dir"
        )
        self.assertEqual(result.work_report_reset_at_utc, "2026-08-10T11:59:59Z")
        self.assertFalse(result.output_truncated)
        self.assertEqual(
            opener.urls[0][0],
            "http://127.0.0.1:8080/api/v1/loader/jobs/safe-job-token",
        )

    def test_loader_job_history_parses_active_and_completed_runs(self):
        opener = RecordingOpener(
            {
                "activeJobId": "running-job",
                "jobs": [
                    {
                        "jobId": "running-job",
                        "createdAtUtc": "2026-08-12T10:00:00Z",
                        "startedAtUtc": "2026-08-12T10:00:01Z",
                        "finishedAtUtc": "",
                        "databaseUser": "FOCUS_APP",
                        "databaseAlias": "FOCUS_HIGH",
                        "status": "running",
                        "rowsInserted": 25,
                        "rowsInsertedKnown": True,
                        "currentRowCount": 125,
                        "currentRowCountKnown": True,
                        "lastFilesLoaded": ["reports/focus-2026-08.csv.gz"],
                    },
                    {
                        "jobId": "old-job",
                        "createdAtUtc": "2026-08-11T10:00:00Z",
                        "startedAtUtc": "2026-08-11T10:00:01Z",
                        "finishedAtUtc": "2026-08-11T10:05:00Z",
                        "databaseUser": "FOCUS_APP_OLD",
                        "databaseAlias": "FOCUS_HIGH",
                        "status": "succeeded",
                        "rowsInserted": 50,
                        "rowsInsertedKnown": True,
                        "currentRowCount": 50,
                        "currentRowCountKnown": True,
                        "lastFilesLoaded": [],
                    },
                ],
            }
        )
        result = FocusAPIClient(
            "http://127.0.0.1:8080", opener=opener
        ).loader_job_history()
        self.assertEqual(result.active_job_id, "running-job")
        self.assertEqual(len(result.jobs), 2)
        self.assertEqual(result.jobs[0].rows_inserted, 25)
        self.assertEqual(
            result.jobs[0].last_files_loaded,
            ("reports/focus-2026-08.csv.gz",),
        )
        self.assertEqual(
            opener.urls[0][0], "http://127.0.0.1:8080/api/v1/loader/jobs"
        )

    def test_loader_job_treats_missing_or_null_analytics_as_empty(self):
        for analytics_fields in (
            {},
            {
                "monthlyCosts": None,
                "services": None,
                "analyticsUpdatedAtUtc": None,
                "analyticsError": None,
            },
        ):
            payload = {
                "jobId": "empty-schema-job",
                "status": "starting",
                "databaseUser": "FOCUS_APP",
                "databaseAlias": "FOCUS_HIGH",
                "tableName": "TEMP_OCI_FOCUS",
                "startedAtUtc": "",
                "finishedAtUtc": "",
                "exitCode": None,
                "initialRowCount": 0,
                "initialRowCountKnown": False,
                "currentRowCount": 0,
                "currentRowCountKnown": False,
                "rowsInserted": 0,
                "rowsInsertedKnown": False,
                "rowCountUpdatedAtUtc": "",
                "rowCountError": "",
                "output": "",
                "error": "",
                **analytics_fields,
            }
            result = FocusAPIClient(
                "http://127.0.0.1:8080", opener=RecordingOpener(payload)
            ).loader_job("empty-schema-job")
            self.assertEqual(result.monthly_costs, ())
            self.assertEqual(result.services, ())
            self.assertEqual(result.analytics_updated_at_utc, "")
            self.assertEqual(result.analytics_error, "")

    def test_loader_job_rejects_unsafe_job_id(self):
        with self.assertRaisesRegex(FocusAPIError, "job id"):
            FocusAPIClient("http://127.0.0.1:8080").loader_job("../unsafe")

    def test_connection_error_is_safe(self):
        def failing_opener(request, timeout):
            raise URLError("connection refused")

        with self.assertRaisesRegex(FocusAPIError, "Cannot reach"):
            FocusAPIClient("http://127.0.0.1:8080", opener=failing_opener).health()


if __name__ == "__main__":
    unittest.main()
