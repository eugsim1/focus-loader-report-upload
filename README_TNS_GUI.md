# Read-only TNS alias web interface

## Recommended implementation

The fastest fit for this project on Oracle Linux 8 is the built-in Go HTTP
interface. It is compiled into the existing `focus-loader-report-upload`
binary and therefore does not require Node.js, Python, Streamlit, a graphical
desktop, X11, or another service runtime.

Start it with `-tns-gui`. This mode exits before OCI and database credential
validation, so `-du`, `-dn`, `-dp`, `-ds`, OCI configuration, and instance
principal permissions are not required merely to display the alias.

The interface:

- reads `TNS_ADMIN` from the server process environment;
- opens exactly `$TNS_ADMIN/tnsnames.ora`;
- skips blank lines, comments, and `IFILE` include directives;
- displays the first alias in the first alias assignment;
- returns the first alias when one descriptor has multiple aliases;
- reads the file again on every page load or **Refresh** click;
- exposes only the alias, file path, read time, and an error if applicable;
- never displays the connect descriptor and never writes `tnsnames.ora`.

The alias input is deliberately read-only. Editing a network configuration
file from a browser would require authentication, authorization, concurrency
control, backups, validation, and an audit trail; none is needed for the stated
display-only requirement.

## 1. Verify `TNS_ADMIN` and the file

Run these checks as the same account that will run the service:

```bash
export TNS_ADMIN=/opt/oracle/wallet
test -d "$TNS_ADMIN"
test -r "$TNS_ADMIN/tnsnames.ora"
grep -nE '^[[:space:]]*[A-Za-z0-9_.-]+([[:space:]]*,[^=]+)?[[:space:]]*=' \
  "$TNS_ADMIN/tnsnames.ora" | head -1
```

For the supplied service account:

```bash
sudo -u focusloader env TNS_ADMIN=/opt/oracle/wallet \
  test -r /opt/oracle/wallet/tnsnames.ora
```

Grant only directory traversal and file-read access required by the
`focusloader` account. Do not make a wallet world-readable.

## 2. Build and test on Oracle Linux 8

The complete application uses `godror`, so install GCC and build with CGO:

```bash
sudo dnf install -y gcc golang
cd /opt/focus-loader/src
go mod download
go mod verify
CGO_ENABLED=1 go test ./...
CGO_ENABLED=1 go build -buildvcs=false -trimpath \
  -o dist/focus-loader-report-upload .
```

Install the executable:

```bash
sudo install -o focusloader -g focusloader -m 0750 \
  dist/focus-loader-report-upload \
  /opt/focus-loader/focus-loader-report-upload
```

## 3. Run it manually

```bash
sudo -u focusloader env TNS_ADMIN=/opt/oracle/wallet \
  /opt/focus-loader/focus-loader-report-upload \
  -tns-gui \
  -tns-gui-listen 127.0.0.1:8080
```

No loader database password or OCI credentials are needed in this mode. Test
the server locally from a second terminal:

```bash
curl -fsS http://127.0.0.1:8080/api/tns-alias
```

Example response:

```json
{
  "alias": "focusdb_high",
  "sourcePath": "/opt/oracle/wallet/tnsnames.ora",
  "readAtUtc": "2026-08-10T12:00:00Z"
}
```

Press `Ctrl+C` to stop the manual server.

## 4. Install the systemd service

The supplied unit binds only to the VM loopback interface:

```bash
cd /opt/focus-loader/src
sudo install -o root -g focusloader -m 0640 \
  deploy/focus-loader-tns-gui.env.example \
  /etc/focus-loader/tns-gui.env
sudo vi /etc/focus-loader/tns-gui.env

sudo install -o root -g root -m 0644 \
  deploy/focus-loader-tns-gui.service.example \
  /etc/systemd/system/focus-loader-tns-gui.service
sudo systemctl daemon-reload
sudo systemctl enable --now focus-loader-tns-gui.service
sudo systemctl status --no-pager focus-loader-tns-gui.service
```

Review the configured value without exposing any wallet content:

```bash
sudo systemctl cat focus-loader-tns-gui.service
sudo grep '^TNS_ADMIN=' /etc/focus-loader/tns-gui.env
sudo -u focusloader bash -c \
  'source /etc/focus-loader/tns-gui.env && test -r "$TNS_ADMIN/tnsnames.ora"'
```

Follow the service logs:

```bash
sudo journalctl -u focus-loader-tns-gui.service -f
```

## 5. Open the page from a workstation

Keeping the service on `127.0.0.1` is the safest and quickest option. Create an
SSH tunnel through the normal VM access path:

```bash
ssh -N -L 8080:127.0.0.1:8080 opc@SERVER_IP
```

Then open:

```text
http://127.0.0.1:8080/
```

When OCI Bastion is required, add `-L 8080:127.0.0.1:8080` to the SSH command
created for the active Bastion managed-SSH session. The local browser URL stays
the same.

Do not bind the page to `0.0.0.0` merely for convenience. The page has no
built-in login. If direct network access is required, place it behind an
authenticated TLS reverse proxy, restrict the NSG/security-list and host
firewall sources, and bind the proxy rather than this process publicly.

## 6. Troubleshooting

### The page says `TNS_ADMIN is not set`

The environment in an interactive shell is not automatically inherited by
systemd. Put `TNS_ADMIN=/actual/directory` in
`/etc/focus-loader/tns-gui.env`, then restart:

```bash
sudo systemctl restart focus-loader-tns-gui.service
sudo journalctl -u focus-loader-tns-gui.service -n 50 --no-pager
```

### The page says it cannot open `tnsnames.ora`

Check the directory, filename case, and permissions as the service account:

```bash
sudo -u focusloader namei -l /opt/oracle/wallet/tnsnames.ora
sudo -u focusloader test -r /opt/oracle/wallet/tnsnames.ora
```

`TNS_ADMIN` must point to the directory. Do not configure it as
`/opt/oracle/wallet/tnsnames.ora`.

### The page says no alias was found

Confirm that the file has a normal top-level alias assignment such as:

```text
focusdb_high =
  (DESCRIPTION = ...)
```

An `IFILE` directive by itself is skipped. The included file is not followed by
this display utility; put at least one service alias in the selected
`tnsnames.ora` or set `TNS_ADMIN` to the intended network-admin directory.

### The browser cannot connect

On the server:

```bash
sudo systemctl is-active focus-loader-tns-gui.service
sudo ss -ltnp | grep ':8080'
curl -v http://127.0.0.1:8080/api/tns-alias
```

On the workstation, confirm that the SSH tunnel is still running and that the
local port is listening. Choose another local port if `8080` is already used:

```bash
ssh -N -L 18080:127.0.0.1:8080 opc@SERVER_IP
```

Then open `http://127.0.0.1:18080/`.

### The service starts, but the alias differs from the loader alias

The GUI shows the first entry by design. The loader uses the value passed with
`-dn`; that alias can be any valid entry in the same file. Compare them with:

```bash
curl -fsS http://127.0.0.1:8080/api/tns-alias
grep -nE '^[[:space:]]*[A-Za-z0-9_.-]+[[:space:]]*=' \
  "$TNS_ADMIN/tnsnames.ora"
```

## Security notes

- Keep the listener on loopback unless a protected reverse proxy is used.
- Do not place wallet contents, credentials, or descriptors in browser logs.
- The handler accepts only `GET`/`HEAD`, adds no-cache and browser-hardening
  headers, and has request timeouts.
- The source path is fixed from the server-side environment; the browser cannot
  request an arbitrary file.
- The implementation is independent software and is not affiliated with,
  endorsed, certified, or supported by Oracle Corporation.
