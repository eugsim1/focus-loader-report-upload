<#
.SYNOPSIS
Authenticates to OCI in a browser, then opens the Streamlit Bastion tunnel.

.DESCRIPTION
Creates a temporary OCI CLI security-token profile using browser login,
validates that profile, and delegates Bastion session and SSH forwarding to
the shared connect-streamlit-bastion.ps1 launcher.
#>

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^ocid1\.bastion\.')]
    [string]$BastionId,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^ocid1\.instance\.')]
    [string]$InstanceId,

    [Parameter(Mandatory = $true)]
    [string]$PrivateIp,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-z]{2}-[a-z0-9-]+-[0-9]+$')]
    [string]$Region,

    [Parameter(Mandatory = $true)]
    [string]$SshPrivateKeyPath,

    [string]$SshPublicKeyPath,

    [ValidatePattern('^[A-Za-z0-9_-]+$')]
    [string]$ProfileName = 'BASTION',

    [string]$OciConfigFilePath,

    [string]$TenancyName,

    [string]$IdentityProviderName,

    [ValidateRange(5, 60)]
    [int]$SessionExpirationMinutes = 60,

    [ValidateRange(30, 10800)]
    [int]$BastionSessionTtl = 3600,

    [ValidateRange(1, 65535)]
    [int]$LocalPort = 8501,

    [ValidateRange(1, 65535)]
    [int]$RemotePort = 8501,

    [ValidateRange(60, 3600)]
    [int]$WaitSeconds = 1200,

    [ValidateRange(1, 60)]
    [int]$PollSeconds = 10,

    [ValidatePattern('^[a-z_][a-z0-9_-]*$')]
    [string]$TargetUser = 'oracle',

    [string]$OciExecutable = 'oci',

    [string]$SshExecutable = 'ssh',

    [switch]$UseExistingSession,

    [switch]$KeepSession,

    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Resolve-ExecutablePath {
    param([Parameter(Mandatory = $true)][string]$Name)
    $command = Get-Command -Name $Name -CommandType Application -ErrorAction Stop |
        Select-Object -First 1
    if ($command.Path) {
        return $command.Path
    }
    return $command.Source
}

function Invoke-NativeText {
    param(
        [Parameter(Mandatory = $true)][string]$Executable,
        [Parameter(Mandatory = $true)][string[]]$Arguments
    )

    Write-Verbose ("Running: {0} {1}" -f $Executable, ($Arguments -join ' '))
    $previousErrorActionPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        $records = @(& $Executable @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }

    $stdoutLines = New-Object 'System.Collections.Generic.List[string]'
    $stderrLines = New-Object 'System.Collections.Generic.List[string]'
    foreach ($record in $records) {
        if ($record -is [System.Management.Automation.ErrorRecord]) {
            $stderrLines.Add($record.ToString().TrimEnd())
        }
        else {
            $stdoutLines.Add($record.ToString())
        }
    }
    $stdout = ($stdoutLines -join [Environment]::NewLine).Trim()
    $stderr = ($stderrLines -join [Environment]::NewLine).Trim()
    if ($exitCode -ne 0) {
        $details = $stderr
        if (-not $details) { $details = $stdout }
        if (-not $details) { $details = 'The command returned no diagnostic output.' }
        throw "Command failed with exit code ${exitCode}: $Executable $($Arguments -join ' ')`n$details"
    }
    if ($stderr) { Write-Warning $stderr }
    return $stdout
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$tunnelScript = Join-Path $repositoryRoot 'linux8-streamlit-bastion\connect-streamlit-bastion.ps1'
if (-not (Test-Path -LiteralPath $tunnelScript -PathType Leaf)) {
    throw "Shared Bastion tunnel launcher is missing: $tunnelScript"
}

$tunnelParameters = @{
    BastionId = $BastionId
    InstanceId = $InstanceId
    PrivateIp = $PrivateIp
    Region = $Region
    SshPrivateKeyPath = $SshPrivateKeyPath
    Profile = $ProfileName
    AuthMode = 'security_token'
    TargetUser = $TargetUser
    SessionTtl = $BastionSessionTtl
    LocalPort = $LocalPort
    RemotePort = $RemotePort
    WaitSeconds = $WaitSeconds
    PollSeconds = $PollSeconds
    OciExecutable = $OciExecutable
    SshExecutable = $SshExecutable
    KeepSession = $KeepSession
    DryRun = $DryRun
}
if ($SshPublicKeyPath) { $tunnelParameters.SshPublicKeyPath = $SshPublicKeyPath }
if ($OciConfigFilePath) { $tunnelParameters.OciConfigFilePath = $OciConfigFilePath }

if ($DryRun) {
    if ($UseExistingSession) {
        Write-Host "DRY RUN: would validate existing security-token profile $ProfileName."
    }
    else {
        Write-Host "DRY RUN: would open OCI browser authentication for profile $ProfileName."
    }
    & $tunnelScript @tunnelParameters
    return
}

$resolvedOciExecutable = Resolve-ExecutablePath -Name $OciExecutable
if (-not $UseExistingSession) {
    $authenticateArguments = @(
        'session', 'authenticate',
        '--region', $Region,
        '--profile-name', $ProfileName,
        '--session-expiration-in-minutes', $SessionExpirationMinutes.ToString()
    )
    if ($OciConfigFilePath) {
        $configDirectory = Split-Path -Parent $OciConfigFilePath
        if ($configDirectory -and -not (Test-Path -LiteralPath $configDirectory)) {
            [void](New-Item -ItemType Directory -Path $configDirectory -Force)
        }
        $authenticateArguments += @('--config-location', $OciConfigFilePath)
    }
    if ($TenancyName) { $authenticateArguments += @('--tenancy-name', $TenancyName) }
    if ($IdentityProviderName) { $authenticateArguments += @('--identity-provider-name', $IdentityProviderName) }

    Write-Host "Opening OCI browser authentication for profile $ProfileName..."
    $authenticationResult = Invoke-NativeText -Executable $resolvedOciExecutable -Arguments $authenticateArguments
    if ($authenticationResult) { Write-Host $authenticationResult }
}

$validateArguments = @(
    'session', 'validate',
    '--profile', $ProfileName,
    '--auth', 'security_token'
)
if ($OciConfigFilePath) {
    $validateArguments += @('--config-file', $OciConfigFilePath)
}
Write-Host "Validating OCI browser session profile $ProfileName..."
$validationResult = Invoke-NativeText -Executable $resolvedOciExecutable -Arguments $validateArguments
if ($validationResult) { Write-Host $validationResult }

& $tunnelScript @tunnelParameters
