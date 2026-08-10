import shlex
import unittest

from command_builder import (
    PASSWORD_PREVIEW,
    CommandValidationError,
    LoaderCommandConfig,
    build_loader_arguments,
    build_loader_command,
    build_loader_execution_payload,
    build_shell_script,
    execution_config_without_password,
)


def base_config(**overrides):
    values = {
        "executable": "/opt/focus-loader/focus-loader-report-upload",
        "oci_auth_mode": "OCI config/profile",
        "database_auth_mode": "Database password",
        "database_user": "FOCUS_GIT1",
        "database_alias": "orapriv063ff3c1_high",
        "database_password": "test-only-password",
        "source_namespace": "bling",
        "oci_profile": "DEFAULT",
        "minimum_date": "2026-01-01",
        "workers": 5,
        "tag_special1": "Oracle-Tags.CreatedBy",
        "tag_special2": "CCA_Basic_Tag.email",
        "tag_special3": "Oracle_Tags.CreatedBy",
        "tag_special4": "Oracle_Tags.CreatedOn",
        "preload_report": True,
        "continue_after_report": True,
        "skip_preload_content_scan": True,
        "skip_tag_rows": True,
    }
    values.update(overrides)
    return LoaderCommandConfig(**values)


class CommandBuilderTests(unittest.TestCase):
    def test_requested_flags_are_dataclass_defaults(self):
        config = LoaderCommandConfig(
            executable="/opt/focus-loader/focus-loader-report-upload",
            oci_auth_mode="OCI config/profile",
            database_auth_mode="Database password",
            database_user="FOCUS_APP",
            database_alias="FOCUS_HIGH",
            database_password="test-only-password",
            source_namespace="bling",
        )
        self.assertTrue(config.preload_report)
        self.assertTrue(config.skip_preload_content_scan)
        self.assertTrue(config.skip_tag_rows)
        self.assertTrue(config.continue_after_report)

    def test_requested_preload_command_fields_and_flags(self):
        config = base_config()
        arguments = build_loader_arguments(config)
        expected_pairs = {
            "-t": "DEFAULT",
            "-du": "FOCUS_GIT1",
            "-dn": "orapriv063ff3c1_high",
            "-dp": PASSWORD_PREVIEW,
            "-ns": "bling",
            "-d": "2026-01-01",
            "-workers": "5",
            "-ts1": "Oracle-Tags.CreatedBy",
            "-ts2": "CCA_Basic_Tag.email",
            "-ts3": "Oracle_Tags.CreatedBy",
            "-ts4": "Oracle_Tags.CreatedOn",
        }
        for flag, value in expected_pairs.items():
            self.assertEqual(arguments[arguments.index(flag) + 1], value)
        for flag in (
            "-preload-report",
            "-skip-preload-content-scan",
            "-skip-tag-rows",
            "-continue-after-report",
        ):
            self.assertIn(flag, arguments)
        self.assertEqual(shlex.split(build_loader_command(config)), arguments)

    def test_password_is_not_saved_in_preview_or_downloaded_script(self):
        config = base_config(database_password="do-not-persist")
        preview = build_loader_command(config)
        script = build_shell_script(config)
        self.assertNotIn("do-not-persist", preview)
        self.assertNotIn("do-not-persist", script)
        self.assertIn(PASSWORD_PREVIEW, preview)
        self.assertIn("read -r -s", script)
        self.assertIn('-dp "${FOCUS_DB_PASSWORD}"', script)

    def test_execution_payload_uses_runtime_password_and_structured_fields(self):
        stored = execution_config_without_password(base_config(database_password="build-only"))
        self.assertEqual(stored.database_password, "")
        payload = build_loader_execution_payload(stored, "runtime-only")
        self.assertEqual(payload["databasePassword"], "runtime-only")
        self.assertEqual(payload["databaseUser"], "FOCUS_GIT1")
        self.assertEqual(payload["workers"], 5)
        self.assertTrue(payload["preloadReport"])
        self.assertTrue(payload["skipPreloadContentScan"])
        self.assertTrue(payload["skipTagRows"])
        self.assertTrue(payload["continueAfterReport"])
        self.assertNotIn("executable", payload)

    def test_vault_secret_authentication(self):
        config = base_config(
            database_auth_mode="OCI Vault secret",
            database_password="",
            vault_secret_ocid="ocid1.vaultsecret.oc1..example",
            vault_secret_profile="DEFAULT",
        )
        arguments = build_loader_arguments(config)
        self.assertNotIn("-dp", arguments)
        self.assertEqual(
            arguments[arguments.index("-ds") + 1], "ocid1.vaultsecret.oc1..example"
        )
        self.assertEqual(arguments[arguments.index("-dst") + 1], "DEFAULT")
        self.assertNotIn("read -r -s", build_shell_script(config))
        payload = build_loader_execution_payload(
            execution_config_without_password(config)
        )
        self.assertEqual(payload["databaseAuthMode"], "vault")
        self.assertEqual(payload["databasePassword"], "")
        self.assertEqual(
            payload["vaultSecretOcid"], "ocid1.vaultsecret.oc1..example"
        )

    def test_instance_principal_authentication(self):
        arguments = build_loader_arguments(
            base_config(oci_auth_mode="Instance principal", oci_profile="")
        )
        self.assertIn("-ip", arguments)
        self.assertNotIn("-t", arguments)

    def test_upload_flags_and_destination_values(self):
        arguments = build_loader_arguments(
            base_config(
                continue_after_report=False,
                upload_reports=True,
                load_after_upload=True,
                destination_bucket="destination",
                destination_prefix="focus curated",
            )
        )
        self.assertIn("-upload-reports", arguments)
        self.assertIn("-load-after-upload", arguments)
        self.assertIn("focus curated", arguments)

    def test_upload_requires_destination(self):
        with self.assertRaisesRegex(CommandValidationError, "destination bucket"):
            build_loader_arguments(
                base_config(continue_after_report=False, upload_reports=True)
            )

    def test_rejects_incompatible_continue_and_upload(self):
        with self.assertRaisesRegex(CommandValidationError, "cannot be combined"):
            build_loader_arguments(
                base_config(upload_reports=True, destination_bucket="destination")
            )

    def test_rejects_missing_selected_database_credential(self):
        with self.assertRaisesRegex(CommandValidationError, "database password"):
            build_loader_arguments(base_config(database_password=""))
        with self.assertRaisesRegex(CommandValidationError, "Vault secret OCID"):
            build_loader_arguments(
                base_config(
                    database_auth_mode="OCI Vault secret",
                    database_password="",
                    vault_secret_ocid="",
                )
            )

    def test_rejects_control_characters(self):
        with self.assertRaisesRegex(CommandValidationError, "control characters"):
            build_loader_arguments(base_config(database_user="FOCUS_APP\n--bad"))

    def test_rejects_invalid_date(self):
        with self.assertRaisesRegex(CommandValidationError, "YYYY-MM-DD"):
            build_loader_arguments(base_config(minimum_date="10/08/2026"))


if __name__ == "__main__":
    unittest.main()
