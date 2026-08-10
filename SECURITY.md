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
- Keep the optional Go API and Streamlit frontend bound to loopback unless they are protected by an authenticated TLS reverse proxy and restricted network policy.
- The Streamlit command builder is preview-only. The separate deployment API is
  limited to one installed script, requires a successful database login plus a
  random 15-minute one-use token, and accepts no command or script path from the
  browser. Keep database/OS auditing and approved change controls enabled.
- The command builder's direct-password field is masked and cleared. Its value
  is validated only for the current render and is never placed in the preview,
  downloaded script, logs, or saved result; the generated script prompts again
  in the terminal. Prefer OCI Vault for scheduled or shared operation.
- Enter database passwords only in masked forms over
  the protected loopback SSH/OCI Bastion path. Never enter a private key, wallet
  content, OCI API key, or Vault secret value in the Streamlit interface.

This software is provided without warranty and is not affiliated with, endorsed, certified, or supported by Oracle Corporation. Use it at your own risk.
