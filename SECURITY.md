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
- The command builder only validates/previews. The separate deployment API is
  limited to one installed script and a random 15-minute one-use token. The
  execution API accepts structured fields only, starts one fixed installed
  loader at a time, restricts aliases to the server TNS catalog, and enforces a
  24-hour timeout. Neither endpoint accepts browser-supplied shell text or an
  executable/script path. Keep database/OS auditing and approved change controls
  enabled.
- The command builder's direct-password field is masked and cleared. Its value
  is validated only for the current render and is never placed in the preview,
  downloaded script, logs, or saved result; the generated script prompts again
  in the terminal. Prefer OCI Vault for scheduled or shared operation.
- The execution tab asks for a direct password again, sends it only over the
  protected loopback connection, and the Go backend passes it to the child over
  stdin rather than command arguments. Direct or Vault-resolved passwords remain
  in backend memory only while required for execution/row/analytics monitoring and are
  redacted from bounded final output.
- Live monitoring runs only fixed row-count, monthly-effective-cost, and
  distinct-service queries against `TEMP_OCI_FOCUS` as the selected database
  user. It reports committed rows, monthly totals grouped by billing currency,
  and unique services; it never accepts browser-supplied SQL, table names,
  schemas, or aliases.
- Enter database passwords only in masked forms over
  the protected loopback SSH/OCI Bastion path. Never enter a private key, wallet
  content, OCI API key, or Vault secret value in the Streamlit interface.

This software is provided without warranty and is not affiliated with, endorsed, certified, or supported by Oracle Corporation. Use it at your own risk.
