<#
.SYNOPSIS
Configures an OCI API-key profile and opens the Streamlit Bastion tunnel.

.DESCRIPTION
Creates or validates a named profile in the local Windows OCI configuration
file using parameter-supplied user, tenancy, fingerprint, region, and API
signing-key path. It then delegates Bastion session creation and SSH forwarding
to the shared connect-streamlit-bastion.ps1 launcher.
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

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^ocid1\.user\.')]
    [string]$OciUserId,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^ocid1\.tenancy\.')]
    [string]$OciTenancyId,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^([0-9A-Fa-f]{2}:){15}[0-9A-Fa-f]{2}$')]
    [string]$ApiKeyFingerprint,

    [Parameter(Mandatory = $true)]
    [string]$ApiPrivateKeyPath,

    [ValidatePattern('^[A-Za-z0-9_-]+$')]
    [string]$ProfileName = 'STREAMLIT_API_KEY',

    [string]$OciConfigFilePath,

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

    [switch]$ReplaceExistingProfile,

    [switch]$KeepSession,

    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not $OciConfigFilePath) {
    $OciConfigFilePath = Join-Path $env:USERPROFILE '.oci\config'
}
$resolvedApiPrivateKey = (Resolve-Path -LiteralPath $ApiPrivateKeyPath -ErrorAction Stop).Path
if (-not (Test-Path -LiteralPath $resolvedApiPrivateKey -PathType Leaf)) {
    throw "ApiPrivateKeyPath is not a file: $resolvedApiPrivateKey"
}
$configPath = [System.IO.Path]::GetFullPath($OciConfigFilePath)

function Find-ProfileRange {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [AllowEmptyString()]
        [string[]]$Lines,
        [Parameter(Mandatory = $true)][string]$Name
    )

    $start = -1
    $end = $Lines.Count
    for ($index = 0; $index -lt $Lines.Count; $index++) {
        if ($Lines[$index] -match '^\s*\[([^]]+)\]\s*$') {
            $sectionName = $Matches[1]
            if ($start -ge 0) {
                $end = $index
                break
            }
            if ($sectionName -eq $Name) {
                $start = $index
            }
        }
    }
    return [pscustomobject]@{ Start = $start; End = $end }
}

function Read-ProfileValues {
    param(
        [Parameter(Mandatory = $true)]
        [AllowEmptyCollection()]
        [AllowEmptyString()]
        [string[]]$Lines,
        [Parameter(Mandatory = $true)][int]$Start,
        [Parameter(Mandatory = $true)][int]$End
    )

    $values = @{}
    for ($index = $Start + 1; $index -lt $End; $index++) {
        if ($Lines[$index] -match '^\s*([^#;][^=]*)=(.*)$') {
            $values[$Matches[1].Trim()] = $Matches[2].Trim()
        }
    }
    return $values
}

function Write-ApiKeyProfile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][hashtable]$Values,
        [switch]$Replace
    )

    $lines = [string[]]@()
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        $lines = [System.IO.File]::ReadAllLines($Path)
    }
    $range = Find-ProfileRange -Lines $lines -Name $Name
    if ($range.Start -ge 0) {
        $currentValues = Read-ProfileValues -Lines $lines -Start $range.Start -End $range.End
        $matchesExpected = $true
        foreach ($key in $Values.Keys) {
            if (-not $currentValues.ContainsKey($key) -or $currentValues[$key] -ne $Values[$key]) {
                $matchesExpected = $false
                break
            }
        }
        if ($matchesExpected) {
            Write-Host "OCI profile $Name already matches the supplied parameters."
            return
        }
        if (-not $Replace) {
            throw "OCI profile $Name already exists with different values. Review it or rerun with -ReplaceExistingProfile."
        }
    }

    $profileLines = @(
        "[$Name]",
        "user=$($Values.user)",
        "fingerprint=$($Values.fingerprint)",
        "key_file=$($Values.key_file)",
        "tenancy=$($Values.tenancy)",
        "region=$($Values.region)"
    )
    $updatedLines = New-Object 'System.Collections.Generic.List[string]'
    if ($range.Start -ge 0) {
        for ($index = 0; $index -lt $range.Start; $index++) {
            $updatedLines.Add($lines[$index])
        }
        foreach ($line in $profileLines) { $updatedLines.Add($line) }
        for ($index = $range.End; $index -lt $lines.Count; $index++) {
            $updatedLines.Add($lines[$index])
        }
    }
    else {
        foreach ($line in $lines) { $updatedLines.Add($line) }
        if ($updatedLines.Count -gt 0 -and $updatedLines[$updatedLines.Count - 1] -ne '') {
            $updatedLines.Add('')
        }
        foreach ($line in $profileLines) { $updatedLines.Add($line) }
    }

    $configDirectory = Split-Path -Parent $Path
    if ($configDirectory -and -not (Test-Path -LiteralPath $configDirectory)) {
        [void](New-Item -ItemType Directory -Path $configDirectory -Force)
    }
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        $timestamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
        $backupPath = "${Path}.backup-${timestamp}-${PID}"
        Copy-Item -LiteralPath $Path -Destination $backupPath -ErrorAction Stop
        Write-Host "Backed up existing OCI configuration: $backupPath"
    }
    $utf8WithoutBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllLines($Path, $updatedLines, $utf8WithoutBom)
    Write-Host "Configured OCI API-key profile $Name in $Path"
}

$profileValues = @{
    user = $OciUserId
    fingerprint = $ApiKeyFingerprint.ToLowerInvariant()
    key_file = $resolvedApiPrivateKey
    tenancy = $OciTenancyId
    region = $Region
}

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$tunnelScript = Join-Path $repositoryRoot 'linux8-streamlit-bastion\connect-streamlit-bastion.ps1'
if (-not (Test-Path -LiteralPath $tunnelScript -PathType Leaf)) {
    throw "Shared Bastion tunnel launcher is missing: $tunnelScript"
}

if ($DryRun) {
    Write-Host "DRY RUN: would configure profile $ProfileName in $configPath with:"
    Write-Host "  user=$OciUserId"
    Write-Host "  fingerprint=$($ApiKeyFingerprint.ToLowerInvariant())"
    Write-Host "  key_file=$resolvedApiPrivateKey"
    Write-Host "  tenancy=$OciTenancyId"
    Write-Host "  region=$Region"
}
else {
    Write-ApiKeyProfile -Path $configPath -Name $ProfileName -Values $profileValues `
        -Replace:$ReplaceExistingProfile
}

$tunnelParameters = @{
    BastionId = $BastionId
    InstanceId = $InstanceId
    PrivateIp = $PrivateIp
    Region = $Region
    SshPrivateKeyPath = $SshPrivateKeyPath
    Profile = $ProfileName
    OciConfigFilePath = $configPath
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

& $tunnelScript @tunnelParameters
