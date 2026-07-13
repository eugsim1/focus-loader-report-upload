# Security Policy

## Reporting a vulnerability

Do not report suspected vulnerabilities in a public issue. Use GitHub's private security-advisory feature for this repository.

## Safe operation

- Prefer instance principals and OCI Vault over API keys and plaintext database passwords.
- Apply least-privilege IAM permissions to the source reports, destination bucket, compartments, and Vault secrets.
- Never commit OCI private keys, wallets, populated environment files, passwords, secret values, real tenancy-specific configuration, or generated cost reports.
- Treat FOCUS reports, resource OCIDs, tags, database connection information, checkpoints, and audit files as potentially sensitive.
- Protect checkpoints and back them up before upgrades or recovery operations.
- Review schema-deployment scripts before execution; destructive options must be tested in a disposable environment first.
- Validate transformed totals against OCI Cost Analysis and official cost reports before using the output for financial decisions.

This software is provided without warranty and is not affiliated with, endorsed, certified, or supported by Oracle Corporation. Use it at your own risk.
