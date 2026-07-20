# Security

Do not commit database passwords, OCI API keys, Autonomous Database wallets,
OCI Vault secret OCIDs, generated SQL*Loader files, or local `.env` files.

If a credential is committed, revoke or rotate it immediately and remove it
from the repository history. Replacing it only in the latest commit is not
sufficient.

