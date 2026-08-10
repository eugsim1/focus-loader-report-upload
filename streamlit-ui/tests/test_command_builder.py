import shlex
import unittest

from command_builder import (
    CommandValidationError,
    LoaderCommandConfig,
    build_loader_arguments,
    build_loader_command,
)


def base_config(**overrides):
    values = {
        "executable": "/opt/focus-loader/focus-loader-report-upload",
        "mode": "Pre-load report",
        "auth_mode": "Instance principal",
        "database_user": "FOCUS_APP",
        "database_alias": "focusdb_high",
        "vault_secret_ocid": "ocid1.vaultsecret.oc1..example",
        "source_namespace": "bling",
        "source_bucket": "source bucket",
        "minimum_date": "2026-01-01",
        "workers": 4,
    }
    values.update(overrides)
    return LoaderCommandConfig(**values)


class CommandBuilderTests(unittest.TestCase):
    def test_preload_instance_principal(self):
        config = base_config()
        arguments = build_loader_arguments(config)
        self.assertIn("-ip", arguments)
        self.assertIn("-preload-report", arguments)
        self.assertEqual(shlex.split(build_loader_command(config)), arguments)

    def test_upload_and_load(self):
        arguments = build_loader_arguments(
            base_config(
                mode="Upload and load",
                destination_bucket="destination",
                destination_prefix="focus curated",
            )
        )
        self.assertIn("-upload-reports", arguments)
        self.assertIn("-load-after-upload", arguments)
        self.assertIn("focus curated", arguments)

    def test_config_file_authentication(self):
        arguments = build_loader_arguments(
            base_config(
                auth_mode="OCI config file",
                oci_config_file="/home/focusloader/.oci/config",
                oci_profile="FINOPS",
            )
        )
        self.assertNotIn("-ip", arguments)
        self.assertIn("/home/focusloader/.oci/config", arguments)
        self.assertIn("FINOPS", arguments)

    def test_upload_requires_destination(self):
        with self.assertRaisesRegex(CommandValidationError, "destination bucket"):
            build_loader_arguments(base_config(mode="Upload only"))

    def test_rejects_control_characters(self):
        with self.assertRaisesRegex(CommandValidationError, "control characters"):
            build_loader_arguments(base_config(database_user="FOCUS_APP\n--bad"))

    def test_rejects_invalid_date(self):
        with self.assertRaisesRegex(CommandValidationError, "YYYY-MM-DD"):
            build_loader_arguments(base_config(minimum_date="10/08/2026"))


if __name__ == "__main__":
    unittest.main()
