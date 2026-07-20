# Ansible build, schema deployment, and loader execution

This playbook reproduces the manual build, schema deployment, and FOCUS loader
commands on the target Oracle Linux host. Non-secret settings are in
`group_vars/all/main.yml`; password-bearing tasks use `no_log: true`.

## 1. Prepare inventory

```bash
cd ansible
cp inventory/hosts.yml.example inventory/hosts.yml
```

Edit `inventory/hosts.yml` with the target private IP, Oracle user's SSH key,
and the active OCI Bastion `ProxyCommand`. If Terraform already generates the
inventory, use that generated file instead.

## 2. Set variables

Edit `group_vars/all/main.yml`. Verify the project path, wallet path, database
alias, schema, namespace, minimum date, worker count, and tag-column values.

## 3. Supply secrets

For a one-time run, export them on the Ansible controller:

```bash
export DB_ADMIN_PASSWORD='replace-me'
export TARGET_SCHEMA_PASSWORD='replace-me'
export FOCUS_DB_PASSWORD='replace-me'
```

Avoid putting real passwords directly in shell history. For a reusable setup:

```bash
cp group_vars/all/vault.yml.example group_vars/all/vault.yml
ansible-vault encrypt group_vars/all/vault.yml
```

The loader can avoid `-dp` when `focus_db_secret_id` and
`focus_db_secret_profile` identify a suitable OCI Vault secret.

## 4. Test connectivity and run

```bash
ansible all -m ping
ansible-playbook site.yml --ask-vault-pass
```

Omit `--ask-vault-pass` when secrets come from controller environment
variables. Add diagnostics when needed:

```bash
ansible-playbook site.yml -vv
```

Before running the loader, the playbook verifies that
`sql_scripts/focus.conf` and the copied parent `focus.conf` have identical
SHA-256 checksums.
