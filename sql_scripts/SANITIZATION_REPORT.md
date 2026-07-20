# Sanitization report

The distribution was produced from a private working snapshot. The source was
left unchanged.

Removed or replaced:

- plaintext database and schema passwords;
- hard-coded wallet directories, TNS aliases, usernames, and schema names;
- environment-specific application paths;
- the generated schema column dictionary CSV;
- password-bearing SQLPlus command-line arguments.

Added safeguards:

- `.env.example` with placeholders only;
- `.gitignore` rules for credentials, wallets, generated CSV files, and logs;
- environment-variable validation and schema identifier validation;
- SQLPlus `/nolog` connections through stdin with substitution disabled;
- `SECURITY.md` and setup documentation.

Verification performed:

- Bash syntax checks for every `.sh` file;
- scans for the known credentials, wallet/service/schema values, private-key
  markers, OCI resource identifiers, and password-bearing SQLPlus arguments;
- manifest review confirming that no wallet, key, local `.env`, Git metadata,
  or generated database export is included.

