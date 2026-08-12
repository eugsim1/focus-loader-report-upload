# Linux browser authentication and multi-port Bastion tunnel

This is the Linux equivalent of the Windows browser-authentication connector.
OCI CLI opens the local graphical browser to create a temporary security-token
profile. The launcher then creates a managed-SSH Bastion session and forwards
remote ports 22, 8501, 8502, and 5901, plus two optional ports, to loopback.

Browser authentication requires a Linux desktop session with a browser. On a
headless server, use the API-key launcher or authenticate on a workstation with
an approved graphical session.

## Install and configure

```bash
cd /path/to/focus-loader-report-upload-source
chmod 0755 linux-browser-auth-streamlit/connect-streamlit-browser-auth.sh \
  scripts/oci-bastion-tunnel-common.sh
cp linux-browser-auth-streamlit/output_settings.example.txt \
  linux-browser-auth-streamlit/output_settings.txt
chmod 0600 linux-browser-auth-streamlit/output_settings.txt
vi linux-browser-auth-streamlit/output_settings.txt
```

The example uses `SshLocalPort=2222` because unprivileged Linux users normally
cannot bind port 22. `OptionalPort1` and `OptionalPort2` accept same-number
ports or `0` to disable them. All forwards bind only to `127.0.0.1`.

## One-line commands

New browser authentication:

```bash
cd /path/to/focus-loader-report-upload-source && ./linux-browser-auth-streamlit/connect-streamlit-browser-auth.sh --parameter-file ./linux-browser-auth-streamlit/output_settings.txt
```

Reuse a valid security-token profile:

```bash
cd /path/to/focus-loader-report-upload-source && ./linux-browser-auth-streamlit/connect-streamlit-browser-auth.sh --parameter-file ./linux-browser-auth-streamlit/output_settings.txt --use-existing-session
```

Dry run:

```bash
cd /path/to/focus-loader-report-upload-source && ./linux-browser-auth-streamlit/connect-streamlit-browser-auth.sh --parameter-file ./linux-browser-auth-streamlit/output_settings.txt --dry-run
```

Keep the terminal open. Use `ssh -p 2222 oracle@127.0.0.1`, browse to ports
8501/8502, or connect a VNC client to loopback port 5901 when those remote
services exist. Ctrl+C closes all forwards and deletes the temporary Bastion
session unless `--keep-session` is supplied.
