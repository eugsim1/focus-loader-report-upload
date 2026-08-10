"""Validated Oracle Linux command preview for the FOCUS Loader CLI."""

from __future__ import annotations

import shlex
from dataclasses import dataclass, replace
from datetime import date


class CommandValidationError(ValueError):
    pass


@dataclass(frozen=True)
class LoaderCommandConfig:
    executable: str
    oci_auth_mode: str
    database_auth_mode: str
    database_user: str
    database_alias: str
    source_namespace: str
    database_password: str = ""
    vault_secret_ocid: str = ""
    vault_secret_profile: str = ""
    source_bucket: str = ""
    oci_config_file: str = ""
    oci_profile: str = "DEFAULT"
    minimum_date: str = ""
    workers: int = 1
    exact_object: str = ""
    tag_special1: str = ""
    tag_special2: str = ""
    tag_special3: str = ""
    tag_special4: str = ""
    destination_namespace: str = ""
    destination_bucket: str = ""
    destination_prefix: str = ""
    preload_report: bool = True
    continue_after_report: bool = True
    skip_preload_content_scan: bool = True
    upload_reports: bool = False
    load_after_upload: bool = False
    force: bool = False
    skip_tags: bool = False
    skip_tag_rows: bool = True
    skip_tag_keys: bool = False
    keep_work_files: bool = False
    verbose: bool = False


VALID_OCI_AUTH_MODES = {"OCI config/profile", "Instance principal"}
VALID_DATABASE_AUTH_MODES = {"Database password", "OCI Vault secret"}
PASSWORD_PREVIEW = "[DATABASE_PASSWORD_PROMPT]"
PASSWORD_SCRIPT_SENTINEL = "__FOCUS_DB_PASSWORD_FROM_SECURE_PROMPT__"


def build_loader_arguments(
    config: LoaderCommandConfig,
    direct_password_value: str = PASSWORD_PREVIEW,
) -> list[str]:
    _validate(config)
    arguments = [config.executable]

    if config.oci_auth_mode == "Instance principal":
        arguments.append("-ip")
    else:
        if config.oci_config_file:
            arguments.extend(["-c", config.oci_config_file])
        arguments.extend(["-t", config.oci_profile])

    arguments.extend(["-du", config.database_user, "-dn", config.database_alias])
    if config.database_auth_mode == "Database password":
        arguments.extend(["-dp", direct_password_value])
    else:
        arguments.extend(["-ds", config.vault_secret_ocid])
        if config.vault_secret_profile:
            arguments.extend(["-dst", config.vault_secret_profile])

    arguments.extend(["-ns", config.source_namespace])
    if config.source_bucket:
        arguments.extend(["-bn", config.source_bucket])
    if config.minimum_date:
        arguments.extend(["-d", config.minimum_date])
    arguments.extend(["-workers", str(config.workers)])
    if config.exact_object:
        arguments.extend(["-f", config.exact_object])

    for value, flag in (
        (config.tag_special1, "-ts1"),
        (config.tag_special2, "-ts2"),
        (config.tag_special3, "-ts3"),
        (config.tag_special4, "-ts4"),
    ):
        if value:
            arguments.extend([flag, value])

    if config.upload_reports:
        arguments.extend(["-report-upload-bucket", config.destination_bucket])
        if config.destination_namespace:
            arguments.extend(["-report-upload-namespace", config.destination_namespace])
        if config.destination_prefix:
            arguments.extend(["-report-upload-prefix", config.destination_prefix])

    for enabled, flag in (
        (config.preload_report, "-preload-report"),
        (config.continue_after_report, "-continue-after-report"),
        (config.skip_preload_content_scan, "-skip-preload-content-scan"),
        (config.upload_reports, "-upload-reports"),
        (config.load_after_upload, "-load-after-upload"),
        (config.force, "-force"),
        (config.skip_tags, "-skip-tags"),
        (config.skip_tag_rows, "-skip-tag-rows"),
        (config.skip_tag_keys, "-skip-tag-keys"),
        (config.keep_work_files, "-keep-work-files"),
        (config.verbose, "-verbose"),
    ):
        if enabled:
            arguments.append(flag)
    return arguments


def build_loader_command(config: LoaderCommandConfig) -> str:
    """Return a safe preview that never contains the submitted password."""

    return shlex.join(build_loader_arguments(config))


def execution_config_without_password(config: LoaderCommandConfig) -> LoaderCommandConfig:
    """Return the validated configuration without retaining a direct password."""

    _validate(config)
    return replace(config, database_password="")


def build_loader_execution_payload(
    config: LoaderCommandConfig,
    database_password: str = "",
) -> dict[str, object]:
    """Build the structured job request; the backend controls the executable."""

    runtime_config = replace(
        config,
        database_password=(
            database_password if config.database_auth_mode == "Database password" else ""
        ),
    )
    _validate(runtime_config)
    return {
        "ociAuthMode": (
            "instance_principal"
            if runtime_config.oci_auth_mode == "Instance principal"
            else "config_profile"
        ),
        "databaseAuthMode": (
            "vault"
            if runtime_config.database_auth_mode == "OCI Vault secret"
            else "password"
        ),
        "ociConfigFile": runtime_config.oci_config_file,
        "ociProfile": runtime_config.oci_profile,
        "databaseUser": runtime_config.database_user,
        "databaseAlias": runtime_config.database_alias,
        "databasePassword": runtime_config.database_password,
        "vaultSecretOcid": (
            runtime_config.vault_secret_ocid
            if runtime_config.database_auth_mode == "OCI Vault secret"
            else ""
        ),
        "vaultSecretProfile": (
            runtime_config.vault_secret_profile
            if runtime_config.database_auth_mode == "OCI Vault secret"
            else ""
        ),
        "sourceNamespace": runtime_config.source_namespace,
        "sourceBucket": runtime_config.source_bucket,
        "minimumDate": runtime_config.minimum_date,
        "workers": runtime_config.workers,
        "exactObject": runtime_config.exact_object,
        "tagSpecial1": runtime_config.tag_special1,
        "tagSpecial2": runtime_config.tag_special2,
        "tagSpecial3": runtime_config.tag_special3,
        "tagSpecial4": runtime_config.tag_special4,
        "destinationNamespace": runtime_config.destination_namespace,
        "destinationBucket": runtime_config.destination_bucket,
        "destinationPrefix": runtime_config.destination_prefix,
        "preloadReport": runtime_config.preload_report,
        "continueAfterReport": runtime_config.continue_after_report,
        "skipPreloadContentScan": runtime_config.skip_preload_content_scan,
        "uploadReports": runtime_config.upload_reports,
        "loadAfterUpload": runtime_config.load_after_upload,
        "force": runtime_config.force,
        "skipTags": runtime_config.skip_tags,
        "skipTagRows": runtime_config.skip_tag_rows,
        "skipTagKeys": runtime_config.skip_tag_keys,
        "keepWorkFiles": runtime_config.keep_work_files,
        "verbose": runtime_config.verbose,
    }


def build_shell_script(config: LoaderCommandConfig) -> str:
    """Return a runnable script without persisting the submitted password."""

    if config.database_auth_mode != "Database password":
        return "#!/usr/bin/env bash\nset -euo pipefail\n\n" + build_loader_command(config) + "\n"

    arguments = build_loader_arguments(config, PASSWORD_SCRIPT_SENTINEL)
    command_parts = [
        '"${FOCUS_DB_PASSWORD}"' if value == PASSWORD_SCRIPT_SENTINEL else shlex.quote(value)
        for value in arguments
    ]
    return (
        "#!/usr/bin/env bash\n"
        "set -euo pipefail\n\n"
        "IFS= read -r -s -p 'Database password: ' FOCUS_DB_PASSWORD\n"
        "printf '\\n'\n"
        "trap 'unset FOCUS_DB_PASSWORD' EXIT\n\n"
        + " ".join(command_parts)
        + "\n"
    )


def _validate(config: LoaderCommandConfig) -> None:
    string_values = {
        "executable": config.executable,
        "database user": config.database_user,
        "database alias": config.database_alias,
        "database password": config.database_password,
        "Vault secret OCID": config.vault_secret_ocid,
        "Vault secret profile": config.vault_secret_profile,
        "source namespace": config.source_namespace,
        "source bucket": config.source_bucket,
        "OCI config file": config.oci_config_file,
        "OCI profile": config.oci_profile,
        "minimum date": config.minimum_date,
        "exact object": config.exact_object,
        "tag special 1": config.tag_special1,
        "tag special 2": config.tag_special2,
        "tag special 3": config.tag_special3,
        "tag special 4": config.tag_special4,
        "destination namespace": config.destination_namespace,
        "destination bucket": config.destination_bucket,
        "destination prefix": config.destination_prefix,
    }
    for label, value in string_values.items():
        if "\x00" in value or "\n" in value or "\r" in value:
            raise CommandValidationError(f"{label} cannot contain control characters.")

    for label in ("executable", "database user", "database alias", "source namespace"):
        if not string_values[label].strip():
            raise CommandValidationError(f"{label} is required.")
    if config.oci_auth_mode not in VALID_OCI_AUTH_MODES:
        raise CommandValidationError("Unknown OCI authentication mode.")
    if config.database_auth_mode not in VALID_DATABASE_AUTH_MODES:
        raise CommandValidationError("Unknown database authentication mode.")
    if config.oci_auth_mode == "OCI config/profile" and not config.oci_profile.strip():
        raise CommandValidationError("OCI profile is required for config/profile authentication.")
    if config.database_auth_mode == "Database password" and not config.database_password:
        raise CommandValidationError("database password is required for password authentication.")
    if config.database_auth_mode == "OCI Vault secret" and not config.vault_secret_ocid.strip():
        raise CommandValidationError("Vault secret OCID is required for Vault authentication.")
    if config.workers < 1:
        raise CommandValidationError("workers must be at least 1.")
    if config.upload_reports and not config.destination_bucket.strip():
        raise CommandValidationError("destination bucket is required with upload reports.")
    if config.load_after_upload and not config.upload_reports:
        raise CommandValidationError("load after upload requires upload reports.")
    if config.continue_after_report and config.upload_reports:
        raise CommandValidationError("continue after report cannot be combined with upload reports.")
    if config.minimum_date:
        try:
            date.fromisoformat(config.minimum_date)
        except ValueError as error:
            raise CommandValidationError("minimum date must use YYYY-MM-DD format.") from error
