# Linux API-key authentication and multi-port Bastion tunnel

This launcher is the Linux equivalent of the Windows API-key connector. It
reads a strict `Name=Value` file, safely creates or replaces one OCI API-key
profile, creates a temporary managed-SSH Bastion session, and starts the fixed
loopback forwards for remote ports 22, 8501, 8502, and 5901 plus as many as two
optional same-number ports.

## Install and configure

```bash
cd /path/to/focus-loader-report-upload-source
chmod 0755 linux-api-key-auth-streamlit/connect-streamlit-api-key-auth.sh \
  scripts/oci-bastion-tunnel-common.sh
cp linux-api-key-auth-streamlit/output_settings.example.txt \
  linux-api-key-auth-streamlit/output_settings.txt
chmod 0600 linux-api-key-auth-streamlit/output_settings.txt
vi linux-api-key-auth-streamlit/output_settings.txt
```

Linux ports below 1024 normally require privilege. The example therefore uses
`SshLocalPort=2222`, which maps local 2222 to Compute port 22. Do not run the
entire connector as root merely to bind local port 22.

Set optional ports in the file, for example:

```text
OptionalPort1=8888
OptionalPort2=9090
```

Use `0` to disable either optional port. All listeners bind only to
`127.0.0.1`.

## One-line command

```bash
cd /path/to/focus-loader-report-upload-source && ./linux-api-key-auth-streamlit/connect-streamlit-api-key-auth.sh --parameter-file ./linux-api-key-auth-streamlit/output_settings.txt --replace-existing-profile
```

Dry run:

```bash
cd /path/to/focus-loader-report-upload-source && ./linux-api-key-auth-streamlit/connect-streamlit-api-key-auth.sh --parameter-file ./linux-api-key-auth-streamlit/output_settings.txt --dry-run
```

Keep the terminal open. Use `ssh -p 2222 oracle@127.0.0.1`, open Streamlit at
`http://127.0.0.1:8501/` or `http://127.0.0.1:8502/`, and connect a loopback VNC
client to `127.0.0.1:5901` only when a secured VNC service is actually running
on the Compute instance. Ctrl+C closes all forwards and deletes the temporary
Bastion session unless `--keep-session` is used.

The parameter file and OCI key material must not be committed. The connector
backs up an existing OCI config before a permitted profile replacement and
writes the updated config with mode 0600.
