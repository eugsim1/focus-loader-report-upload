"""Validated Oracle Linux command preview for the FOCUS Loader CLI."""

from __future__ import annotations

import shlex
from dataclasses import dataclass
from datetime import date


class CommandValidationError(ValueError):
    pass


@dataclass(frozen=True)
class LoaderCommandConfig:
    executable: str
    mode: str
    auth_mode: str
    database_user: str
    database_alias: str
    vault_secret_ocid: str
    source_namespace: str
    source_bucket: str = ""
    oci_config_file: str = ""
    oci_profile: str = "DEFAULT"
    minimum_date: str = ""
    workers: int = 1
    destination_namespace: str = ""
    destination_bucket: str = ""
    destination_prefix: str = ""
    exact_object: str = ""
    force: bool = False
    skip_tags: bool = False
    keep_work_files: bool = False
    verbose: bool = False


VALID_MODES = {
    "Database load",
    "Pre-load report",
    "Upload only",
    "Upload and load",
}
VALID_AUTH_MODES = {"Instance principal", "OCI config file"}


def build_loader_arguments(config: LoaderCommandConfig) -> list[str]:
    _validate(config)
    arguments = [config.executable]

    if config.auth_mode == "Instance principal":
        arguments.append("-ip")
    else:
        arguments.extend(["-c", config.oci_config_file, "-t", config.oci_profile])

    arguments.extend(
        [
            "-du",
            config.database_user,
            "-dn",
            config.database_alias,
            "-ds",
            config.vault_secret_ocid,
            "-ns",
            config.source_namespace,
            "-workers",
            str(config.workers),
        ]
    )
    if config.source_bucket:
        arguments.extend(["-bn", config.source_bucket])
    if config.minimum_date:
        arguments.extend(["-d", config.minimum_date])
    if config.exact_object:
        arguments.extend(["-f", config.exact_object])

    if config.mode == "Pre-load report":
        arguments.append("-preload-report")
    elif config.mode in {"Upload only", "Upload and load"}:
        arguments.extend(["-upload-reports", "-report-upload-bucket", config.destination_bucket])
        if config.destination_namespace:
            arguments.extend(["-report-upload-namespace", config.destination_namespace])
        if config.destination_prefix:
            arguments.extend(["-report-upload-prefix", config.destination_prefix])
        if config.mode == "Upload and load":
            arguments.append("-load-after-upload")

    for enabled, flag in (
        (config.force, "-force"),
        (config.skip_tags, "-skip-tags"),
        (config.keep_work_files, "-keep-work-files"),
        (config.verbose, "-verbose"),
    ):
        if enabled:
            arguments.append(flag)
    return arguments


def build_loader_command(config: LoaderCommandConfig) -> str:
    return shlex.join(build_loader_arguments(config))


def build_shell_script(config: LoaderCommandConfig) -> str:
    command = build_loader_command(config)
    return "#!/usr/bin/env bash\nset -euo pipefail\n\n" + command + "\n"


def _validate(config: LoaderCommandConfig) -> None:
    string_values = {
        "executable": config.executable,
        "database user": config.database_user,
        "database alias": config.database_alias,
        "Vault secret OCID": config.vault_secret_ocid,
        "source namespace": config.source_namespace,
        "source bucket": config.source_bucket,
        "OCI config file": config.oci_config_file,
        "OCI profile": config.oci_profile,
        "destination namespace": config.destination_namespace,
        "destination bucket": config.destination_bucket,
        "destination prefix": config.destination_prefix,
        "exact object": config.exact_object,
    }
    for label, value in string_values.items():
        if "\x00" in value or "\n" in value or "\r" in value:
            raise CommandValidationError(f"{label} cannot contain control characters.")

    for label in ("executable", "database user", "database alias", "Vault secret OCID", "source namespace"):
        if not string_values[label].strip():
            raise CommandValidationError(f"{label} is required.")
    if config.mode not in VALID_MODES:
        raise CommandValidationError("Unknown processing mode.")
    if config.auth_mode not in VALID_AUTH_MODES:
        raise CommandValidationError("Unknown OCI authentication mode.")
    if config.auth_mode == "OCI config file" and not config.oci_config_file.strip():
        raise CommandValidationError("OCI config file is required for config-file authentication.")
    if config.workers < 1:
        raise CommandValidationError("workers must be at least 1.")
    if config.mode in {"Upload only", "Upload and load"} and not config.destination_bucket.strip():
        raise CommandValidationError("destination bucket is required for upload modes.")
    if config.minimum_date:
        try:
            date.fromisoformat(config.minimum_date)
        except ValueError as error:
            raise CommandValidationError("minimum date must use YYYY-MM-DD format.") from error
