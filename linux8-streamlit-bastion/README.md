# Oracle Linux 8 Streamlit deployment through OCI Bastion

This deployment package uses the existing `../streamlit-ui` application.  It
does not expose Streamlit on the VCN or the public internet.  The application
listens only on `127.0.0.1:8501` on the Linux server, and the laptop reaches it
through a local SSH forward over an OCI Bastion **managed SSH** session.

## Result

```text
Laptop browser --> http://127.0.0.1:8501
                    |
                    | SSH local forward over OCI Bastion
                    v
Oracle Linux 8 private IP --> 127.0.0.1:8501 Streamlit
                               127.0.0.1:8080 FOCUS Loader API
```

Do not add an ingress rule for TCP 8501 or TCP 8080.  Do not change either
service to listen on the server private IP or `0.0.0.0`.

## 1. Prerequisites

On the Oracle Linux 8 server, ensure that:

- Oracle Linux is 8.8 or later.
- The server has a private IP and can be reached from an OCI Bastion in the
  same VCN (or through permitted VCN routing).
- `sshd`, the Oracle Cloud Agent, and the Bastion plugin are running.  These
  are required for an OCI Bastion managed SSH session.
- The service account and installed FOCUS Loader binary exist:
  `/opt/focus-loader/focus-loader-report-upload`.
- The `focusloader` account can read `tnsnames.ora` and the Oracle Net/wallet
  files required to connect, commonly `sqlnet.ora` and `cwallet.sso` for ADB.
- Oracle SQL*Plus is installed and executable by `focusloader`, and the loader's
  `/opt/focus-loader/focus.conf` is writable by that account for schema deploys.
- The server can reach your approved Python package repository during setup.

On the server, run:

```bash
sudo dnf install -y python3.11 python3.11-pip curl
sudo useradd --system --create-home --shell /bin/bash focusloader 2>/dev/null || true
python3.11 --version
sudo systemctl status oracle-cloud-agent
sudo systemctl status sshd
```

In the OCI Console, open the instance's **Oracle Cloud Agent** page and verify
that the **Bastion** plugin is enabled and running.

### Least-privilege access to Oracle-owned network files

If `TNS_ADMIN=/home/oracle/adb_wallet` and the wallet must remain owned by
`oracle`, grant `focusloader` access only to the directory path and the common
ADB mTLS runtime files:

```bash
sudo dnf install -y acl

sudo chmod 0700 /home/oracle/adb_wallet
sudo chmod 0600 /home/oracle/adb_wallet/tnsnames.ora
sudo chmod 0600 /home/oracle/adb_wallet/sqlnet.ora
sudo chmod 0600 /home/oracle/adb_wallet/cwallet.sso

# Allow focusloader to traverse the two directories.
sudo setfacl -m u:focusloader:--x /home/oracle
sudo setfacl -m u:focusloader:--x /home/oracle/adb_wallet

# Allow reading the alias and common ADB mTLS runtime files.
sudo setfacl -m u:focusloader:r-- \
  /home/oracle/adb_wallet/tnsnames.ora \
  /home/oracle/adb_wallet/sqlnet.ora \
  /home/oracle/adb_wallet/cwallet.sso
```

Verify the effective access before running the installer:

```bash
sudo -u focusloader test -r /home/oracle/adb_wallet/tnsnames.ora
sudo -u focusloader test -r /home/oracle/adb_wallet/sqlnet.ora
sudo -u focusloader test -r /home/oracle/adb_wallet/cwallet.sso
sudo -u focusloader head -n 1 /home/oracle/adb_wallet/tnsnames.ora
sudo getfacl -p /home/oracle /home/oracle/adb_wallet \
  /home/oracle/adb_wallet/tnsnames.ora
```

An `ls /home/oracle/adb_wallet` command may still return `Permission denied`.
That is expected: directory traversal (`--x`) permits opening the explicitly
named file but does not permit listing the wallet directory. This ACL is enough
for the alias and common ADB mTLS connection path. Wallet contents vary; if
Oracle Net reports another required file, review and grant that file explicitly
rather than making the wallet directory world-readable.

## 2. Copy the source and install the UI

Copy or clone this complete repository onto the server; the installer uses the
existing `streamlit-ui`, `deploy`, and `scripts` folders.  For example, after
copying it to `/opt/focus-loader/src`:

```bash
cd /opt/focus-loader/src

# Build and install the backend that supplies the local TNS/metadata API.
go mod download
go mod verify
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath \
  -o dist/focus-loader-report-upload .
sudo install -o focusloader -g focusloader -m 0750 \
  dist/focus-loader-report-upload /opt/focus-loader/focus-loader-report-upload

# Replace this with the directory that contains tnsnames.ora on your server.
sudo TNS_ADMIN=/opt/oracle/wallet PYTHON_BIN=python3.11 \
  SQLPLUS_BIN=/usr/lib/oracle/23/client64/bin/sqlplus \
  ./scripts/install-streamlit-ui.sh
```

The installer creates two services:

- `focus-loader-tns-gui.service`: local FOCUS Loader API on `127.0.0.1:8080`.
- `focus-loader-streamlit.service`: Streamlit UI on `127.0.0.1:8501`.

It also creates a dedicated Python virtual environment at
`/opt/focus-loader/streamlit-ui/.venv`.

Validate from the Linux server:

```bash
sudo systemctl --no-pager --full status focus-loader-tns-gui.service
sudo systemctl --no-pager --full status focus-loader-streamlit.service
curl -fsS http://127.0.0.1:8080/api/v1/health
curl -fsS http://127.0.0.1:8501/_stcore/health
sudo ss -ltnp | grep -E ':(8080|8501)\\b'
```

Both listeners must show `127.0.0.1`, not the server's private IP.

The first Streamlit tab uses the first TNS alias and accepts `ADMIN` or another
Oracle user plus a masked password. It runs only the built-in `ALL_TABLES`
metadata query; no pre-existing SQL file is needed. The connection closes after
the result is returned.

## 3. Create the session and tunnel from a Windows laptop

The included PowerShell launcher uses the OCI CLI to reuse the existing
Bastion service, create a fresh managed-SSH session, wait for `ACTIVE`, and
open the loopback-only Streamlit tunnel automatically:

```powershell
cd C:\path\to\focus-loader-report-upload\linux8-streamlit-bastion

.\connect-streamlit-bastion.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -Profile 'DEFAULT'
```

The adjacent public key is selected automatically by appending `.pub` to the
private-key path. Use `-SshPublicKeyPath` when it has a different name. The
laptop must have OCI CLI credentials authorized to inspect the Bastion and
manage Bastion sessions, plus the Windows OpenSSH Client:

```powershell
oci iam region-subscription list --profile DEFAULT --all
ssh -V
```

Validate all local arguments without contacting OCI or starting SSH:

```powershell
.\connect-streamlit-bastion.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -DryRun
```

Keep the PowerShell window open and browse to `http://127.0.0.1:8501/`.
Press Ctrl+C to close the tunnel. The script deletes the session it created
when SSH exits; `-KeepSession` leaves it active until its TTL expires. Use
`-LocalPort 18501` if local port 8501 is already occupied.

If Windows blocks local scripts for the current process, use:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

## 4. Create a session manually in OCI Console

In OCI Console:

1. Open **Identity & Security → Bastion**, then choose the bastion in the VCN.
2. Select **Create session** → **Managed SSH session**.
3. Select this Oracle Linux instance as the target and use port `22`.
4. Specify the OS user (normally `opc`) and upload the public SSH key for your
   laptop.
5. Choose the required time-to-live, create the session, then wait until it is
   active.
6. In the session menu, choose **Copy SSH command**.

OCI creates a time-limited command that includes the correct bastion endpoint,
session identity, and key options.  Use that copied command as the source of
truth rather than replacing it with a guessed bastion hostname.

## 5. Open Streamlit manually on the laptop

On the laptop, take the SSH command copied from the managed-SSH session and
add these options immediately after `ssh`:

```text
-N -L 8501:127.0.0.1:8501 -o ServerAliveInterval=120 -o ServerAliveCountMax=3
```

For example, the final command retains every option supplied by OCI but has
this shape:

```bash
ssh -N -L 8501:127.0.0.1:8501 \
  -o ServerAliveInterval=120 -o ServerAliveCountMax=3 \
  <the-rest-of-the-command-copied-from-the-OCI-managed-SSH-session>
```

Keep that terminal open.  Then open this URL on the laptop:

```text
http://127.0.0.1:8501/
```

If port 8501 is already occupied on the laptop, choose another local port such
as 18501 while keeping the remote destination unchanged:

```bash
ssh -N -L 18501:127.0.0.1:8501 <the-rest-of-the-OCI-command>
```

Then browse to `http://127.0.0.1:18501/`.

## 6. Troubleshooting

```bash
# On the server
sudo journalctl -u focus-loader-streamlit.service -n 100 --no-pager
sudo journalctl -u focus-loader-tns-gui.service -n 100 --no-pager
sudo -u focusloader test -r /opt/oracle/wallet/tnsnames.ora
sudo -u focusloader test -r /opt/oracle/wallet/sqlnet.ora
sudo -u focusloader test -r /opt/oracle/wallet/cwallet.sso

# On the laptop: check whether the local forward is listening
netstat -ano | findstr :8501
```

For PowerShell launcher diagnostics, add `-Verbose`. If session creation is
rejected, confirm the OCI CLI profile, Bastion OCID, Compute OCID, target
private IP, session-management IAM policy, Bastion CIDR allowlist, and that the
Compute Bastion plugin is running. The launcher preserves OCI CLI stderr and
prints the complete service diagnostic, including its status code and message.

If the Database tables tab reports an Oracle error, verify the database
username/password, the first alias, the selected schema owner's visibility in
`ALL_TABLES`, Oracle client libraries, and every wallet-file permission. An
`ORA-01017` error normally means invalid credentials; `ORA-12154`, `ORA-12514`,
or TLS/wallet errors normally indicate Oracle Net, wallet, or network setup.

The Deploy schema tab appears only after a successful first-tab login. If its
fixed script fails, review the redacted console output and verify that
`focusloader` can execute SQL*Plus and the installed script and can write both
configuration files:

```bash
sudo -u focusloader bash -c \
  'source /etc/focus-loader/tns-gui.env && command -v sqlplus && sqlplus -version'
sudo -u focusloader test -x \
  /opt/focus-loader/sql_scripts/deploy_focus_schema_with_sqlloader_audit.sh
sudo -u focusloader test -w /opt/focus-loader/sql_scripts/focus.conf
sudo -u focusloader test -w /opt/focus-loader/focus.conf
```

Authenticate again for every deployment because its opaque authorization is
valid for at most 15 minutes and can be used only once. Leave drop-existing
disabled unless an approved change explicitly requires deleting the schema and
all of its objects.

If the managed session cannot be created, confirm the Oracle Cloud Agent and
Bastion plugin status, the instance network route/security rules from the
Bastion subnet to the server's port 22, and that the laptop public IP is in the
Bastion CIDR allowlist.  The Streamlit ports do not need network rules.

For application details, service hardening, manual installation, and rollback,
read [`../streamlit-ui/README.md`](../streamlit-ui/README.md).
