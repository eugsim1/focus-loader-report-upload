# Windows browser authentication and Streamlit tunnel

This package provides the quickest interactive Windows workflow. It opens the
OCI browser login, creates a temporary `security_token` CLI profile, validates
it, creates a managed-SSH session on an existing OCI Bastion, and opens a local
Streamlit tunnel.

The tunnel binds only to `127.0.0.1`. It does not expose ports 8501 or 8080 in
the VCN, NSG, security list, host firewall, or public internet.

## Files

```text
windows-browser-auth-streamlit/
|-- connect-streamlit-browser-auth.ps1
`-- README.md
```

The wrapper delegates the session and tunnel operations to:

```text
linux8-streamlit-bastion/connect-streamlit-bastion.ps1
```

Keep both directories in the same repository checkout.

## Prerequisites

On the Windows laptop:

- Windows PowerShell 5.1 or PowerShell 7;
- OCI CLI installed and available as `oci`;
- Windows OpenSSH Client available as `ssh`;
- a browser able to sign in to the required OCI tenancy;
- the matching SSH private/public key pair used for the Bastion session;
- the existing Bastion OCID, Compute OCID, Compute private IP, and region.

Verify the executables:

```powershell
oci --version
ssh -V
```

The OCI identity needs permission to inspect the Bastion and manage Bastion
sessions for the target instance. The Compute instance must be running, its
OpenSSH server and Oracle Cloud Agent must be running, and its Bastion plugin
must report `RUNNING`.

## First connection

Run from the repository root:

```powershell
.\windows-browser-auth-streamlit\connect-streamlit-browser-auth.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -ProfileName 'BASTION'
```

The script opens the browser login. After authentication it validates the new
profile, creates the managed-SSH session, waits for `ACTIVE`, and starts:

```text
127.0.0.1:8501 -> OCI Bastion -> Compute 127.0.0.1:8501
```

Keep PowerShell open and browse to:

```text
http://127.0.0.1:8501/
```

Press Ctrl+C to stop SSH. The wrapper deletes the Bastion session it created
unless `-KeepSession` is supplied.

## One-line CMD invocation

```cmd
powershell.exe -NoProfile -ExecutionPolicy Bypass -File ".\windows-browser-auth-streamlit\connect-streamlit-browser-auth.ps1" -BastionId "ocid1.bastion.oc1.eu-frankfurt-1.REPLACE" -InstanceId "ocid1.instance.oc1.eu-frankfurt-1.REPLACE" -PrivateIp "10.30.1.10" -Region "eu-frankfurt-1" -SshPrivateKeyPath "C:\Users\YOUR_USER\.ssh\bastion_ed25519" -ProfileName "BASTION"
```

## Reuse or refresh the token

A browser-created OCI CLI session is temporary. To reuse one that is still
valid without opening the browser again:

```powershell
.\windows-browser-auth-streamlit\connect-streamlit-browser-auth.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -ProfileName 'BASTION' `
  -UseExistingSession
```

Validate or refresh it directly:

```powershell
oci session validate --profile BASTION --auth security_token
oci session refresh --profile BASTION
```

If it cannot be refreshed, rerun the wrapper without `-UseExistingSession` to
perform a new browser login.

## Dry run

Validate paths, OCIDs, IP syntax, and tunnel settings without opening a browser,
contacting OCI, creating a session, or starting SSH:

```powershell
.\windows-browser-auth-streamlit\connect-streamlit-browser-auth.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -DryRun
```

## Important parameters

| Parameter | Default | Purpose |
|---|---:|---|
| `-ProfileName` | `BASTION` | Security-token profile created by browser authentication. |
| `-SessionExpirationMinutes` | `60` | OCI login token duration, from 5 through 60 minutes. |
| `-UseExistingSession` | off | Validate and reuse the current token instead of opening a browser. |
| `-OciConfigFilePath` | OCI default | Optional alternate OCI configuration file. |
| `-TenancyName` | empty | Optional tenancy selection for browser authentication. |
| `-IdentityProviderName` | empty | Optional federated identity-provider selection. |
| `-BastionSessionTtl` | `3600` | Managed-SSH session lifetime in seconds. |
| `-LocalPort` | `8501` | Laptop loopback port. Use 18501 if 8501 is busy. |
| `-TargetUser` | `oracle` | Operating-system account on the Compute instance. |
| `-KeepSession` | off | Do not delete the new Bastion session when SSH exits. |
| `-Verbose` | off | Show the OCI commands being executed. |

If the public key is not adjacent to the private key as `<private>.pub`, pass:

```powershell
-SshPublicKeyPath 'C:\Users\YOUR_USER\.ssh\another-name.pub'
```

## Troubleshooting

### PowerShell opens the script in an editor

Force execution:

```cmd
powershell.exe -NoProfile -ExecutionPolicy Bypass -File ".\windows-browser-auth-streamlit\connect-streamlit-browser-auth.ps1" ...
```

### `NotAuthenticated`

The token is missing, invalid, or expired. Authenticate again without
`-UseExistingSession`, or run:

```powershell
oci session validate --profile BASTION --auth security_token
```

### `NotAuthorizedOrNotFound`

Authentication succeeded, but the profile lacks permission or an OCID belongs
to another tenancy, region, or compartment. Confirm the active tenancy and IAM
group policies before changing the script.

### Local port is already used

```powershell
Get-NetTCPConnection -LocalPort 8501 -State Listen
```

Then select another laptop port:

```powershell
-LocalPort 18501
```

Browse to `http://127.0.0.1:18501/`.

### Session becomes ACTIVE but SSH fails

Confirm that the public key matches the supplied SSH private key, the target OS
user exists, the Compute Bastion plugin is running, and Bastion can reach the
private IP on TCP 22.

## Security notes

- The browser token is temporary and stored by OCI CLI under the local user
  profile.
- The SSH private key never leaves the laptop.
- Only its public key is sent when the Bastion session is created.
- Do not commit `.oci`, session tokens, private keys, or copied configuration.
- Keep the local tunnel bound to `127.0.0.1`.

References:

- [OCI CLI browser authentication](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/session/authenticate.html)
- [OCI CLI session refresh](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/session/refresh.html)
- [OCI managed-SSH session creation](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/bastion/session/create-managed-ssh.html)
