# Windows API-key authentication and Streamlit tunnel

This package configures a named OCI CLI profile from explicit API signing-key
parameters, validates it by using the existing Bastion, creates a temporary
managed-SSH session, and opens the laptop's loopback-only Streamlit tunnel.

API-key authentication is appropriate for a secured administrator laptop that
needs a durable OCI CLI profile. Use the browser-authentication package when a
temporary interactive token is preferred.

## Files

```text
windows-api-key-auth-streamlit/
|-- connect-streamlit-api-key-auth.ps1
`-- README.md
```

The wrapper delegates Bastion and SSH operations to:

```text
linux8-streamlit-bastion/connect-streamlit-bastion.ps1
```

## Two different key pairs are involved

Do not confuse these keys:

1. **OCI API signing key**: an RSA PEM private key referenced by the OCI CLI
   profile. Its public key and fingerprint must be registered on the OCI IAM
   user.
2. **Compute/Bastion SSH key**: the OpenSSH key used to connect as `oracle` to
   the Compute instance through the managed-SSH session.

The script accepts separate `-ApiPrivateKeyPath` and `-SshPrivateKeyPath`
parameters and never copies either private key to OCI.

## Prerequisites

- Windows PowerShell 5.1 or PowerShell 7;
- OCI CLI and Windows OpenSSH Client installed;
- an OCI IAM user, tenancy OCID, and API signing-key fingerprint;
- the API signing private key stored locally in PEM format;
- the corresponding public API key registered on the OCI IAM user;
- permission to inspect the existing Bastion and manage Bastion sessions;
- Bastion OCID, Compute OCID, private IP, region, and SSH key pair.

Verify tools:

```powershell
oci --version
ssh -V
```

## Obtain the API profile values

In OCI Console, open your user, select **API keys**, and either add a new API
key or view the configuration file for an existing fingerprint. Record:

- `user`: OCI user OCID;
- `tenancy`: tenancy OCID;
- `fingerprint`: the registered API public-key fingerprint;
- `region`;
- the local path to the matching PEM private key.

Never paste or commit the private-key contents. The API public key registered
in OCI must match the local private key.

## First connection

Run from the repository root:

```powershell
.\windows-api-key-auth-streamlit\connect-streamlit-api-key-auth.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -OciUserId 'ocid1.user.oc1..REPLACE' `
  -OciTenancyId 'ocid1.tenancy.oc1..REPLACE' `
  -ApiKeyFingerprint 'aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99' `
  -ApiPrivateKeyPath "$HOME\.oci\oci_api_key.pem" `
  -ProfileName 'STREAMLIT_API_KEY'
```

By default, the wrapper writes the named profile to:

```text
C:\Users\CURRENT_USER\.oci\config
```

It preserves all other profiles. Before changing an existing configuration it
creates a timestamped backup beside the original file. If the requested profile
already exists with different values, the script stops. Review the difference,
then explicitly allow replacement with `-ReplaceExistingProfile`.

After configuration, the wrapper uses the profile to inspect the Bastion,
create and activate a managed-SSH session, and start:

```text
127.0.0.1:8501 -> OCI Bastion -> Compute 127.0.0.1:8501
```

Keep PowerShell open and browse to `http://127.0.0.1:8501/`. Press Ctrl+C to
close the tunnel. The created Bastion session is deleted unless `-KeepSession`
is supplied.

## One-line CMD invocation

```cmd
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\root\Documents\Codex\2026-07-27\for\work\focus-loader-report-upload-source\windows-api-key-auth-streamlit\connect-streamlit-api-key-auth.ps1" -BastionId "ocid1.bastion.oc1.eu-frankfurt-1.amaaaaaattkvkkiavieclqbepigrnbctut4ievpfm77c2yua3i26ngvegjfa" -InstanceId "ocid1.instance.oc1.eu-frankfurt-1.antheljrttkvkkicjuge3ytieelol5jnyuqa23em37zb7jrucmub7kkuitia" -PrivateIp "10.30.1.159" -Region "eu-frankfurt-1" -SshPrivateKeyPath "C:\Users\root\.ssh\key_03-05-23-17-28" -OciUserId "ocid1.user.oc1..aaaaaaaa2igcu3ynk6fuaber7376plhj546xrb6g4yy3clb4ivimm63jehrq" -OciTenancyId "ocid1.tenancy.oc1..aaaaaaaaxzpxbcag7zgamh2erlggqro3y63tvm2rbkkjz4z2zskvagupiz7a" -ApiKeyFingerprint "03:fe:04:88:48:aa:ce:7b:6b:5c:da:74:63:a0:1d:38" -ApiPrivateKeyPath "C:\eugene\git-oracle\.oci\eugene.simos@2026-06-30T08_13_22.130Z.pem" -ProfileName "STREAMLIT_API_KEY"
```

This command contains deployment-specific OCIDs and local paths. Keep the PEM
private key itself only on the laptop and never add it to the repository.

## Dry run

The dry run validates parameter formats and both local key paths. It prints the
profile and tunnel plan without modifying the OCI config file, contacting OCI,
creating a session, or starting SSH:

```powershell
.\windows-api-key-auth-streamlit\connect-streamlit-api-key-auth.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.REPLACE' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519" `
  -OciUserId 'ocid1.user.oc1..REPLACE' `
  -OciTenancyId 'ocid1.tenancy.oc1..REPLACE' `
  -ApiKeyFingerprint 'aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99' `
  -ApiPrivateKeyPath "$HOME\.oci\oci_api_key.pem" `
  -DryRun
```

## Important parameters

| Parameter | Default | Purpose |
|---|---:|---|
| `-OciUserId` | required | IAM user OCID registered with the public API key. |
| `-OciTenancyId` | required | OCI tenancy OCID. |
| `-ApiKeyFingerprint` | required | 16-byte colon-separated fingerprint shown in OCI. |
| `-ApiPrivateKeyPath` | required | Matching local RSA PEM private-key file. |
| `-ProfileName` | `STREAMLIT_API_KEY` | Named local OCI CLI profile. |
| `-OciConfigFilePath` | `%USERPROFILE%\.oci\config` | Alternate Windows OCI configuration file. |
| `-ReplaceExistingProfile` | off | Replace only the named profile after backing up the config. |
| `-BastionSessionTtl` | `3600` | Managed-SSH session duration in seconds. |
| `-LocalPort` | `8501` | Laptop loopback port. |
| `-TargetUser` | `oracle` | Compute operating-system account. |
| `-KeepSession` | off | Retain the created Bastion session until TTL expiry. |

Use `-SshPublicKeyPath` when the public SSH key is not stored as
`<SshPrivateKeyPath>.pub`.

## Verify the generated profile

Inspect the profile without displaying private-key contents:

```powershell
Get-Content "$HOME\.oci\config"
Test-Path "$HOME\.oci\oci_api_key.pem"
```

Test authentication and Bastion access:

```powershell
oci iam region-subscription list --profile STREAMLIT_API_KEY --all
oci bastion bastion get `
  --bastion-id 'ocid1.bastion.oc1.eu-frankfurt-1.REPLACE' `
  --region eu-frankfurt-1 `
  --profile STREAMLIT_API_KEY
```

## IAM guidance

Use a dedicated OCI group with only the permissions required by your tenancy's
compartment design. A tenancy administrator should confirm policies that allow
the operator to inspect the Bastion, read the target Compute instance, and
manage Bastion sessions in the applicable compartment. Do not grant tenancy-
wide administrator access merely to make the tunnel work.

## Troubleshooting

### `NotAuthenticated` / HTTP 401

Check that:

- the profile has `user`, `fingerprint`, `key_file`, `tenancy`, and `region`;
- `key_file` points to the matching PEM private key;
- the fingerprint is registered on the specified OCI user;
- the system clock is synchronized;
- an encrypted private key can be unlocked when OCI CLI prompts.

The API signing key is not the Compute SSH key.

### `NotAuthorizedOrNotFound`

The request was authenticated, but IAM permission or resource scope is wrong.
Confirm the tenancy, region, compartment, user groups, policies, and OCIDs.

### Existing profile differs

Review the current profile and the automatically generated backup. Replace only
after confirming the supplied OCIDs, fingerprint, region, and key path:

```powershell
-ReplaceExistingProfile
```

### SSH fails after the session is ACTIVE

Confirm the SSH public/private keys match, the target user exists, the Bastion
plugin is running, and the Bastion subnet can reach the Compute private IP on
TCP 22.

## Security notes

- Never commit the API private key, SSH private key, `.oci/config`, or backups.
- Restrict both private keys to the Windows user running the launcher.
- Rotate and remove unused API keys in OCI IAM.
- The wrapper stores key paths, not private-key contents, in the profile.
- The tunnel listens only on laptop loopback.
- The created Bastion session is temporary and deleted on normal exit.

References:

- [OCI SDK and CLI configuration file](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/sdkconfig.htm)
- [OCI required API signing keys and OCIDs](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/apisigningkey.htm)
- [OCI authentication methods](https://docs.oracle.com/en-us/iaas/Content/API/Concepts/sdk_authentication_methods.htm)
- [OCI managed-SSH session creation](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/bastion/session/create-managed-ssh.html)
