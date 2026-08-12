<#
.SYNOPSIS
Configures an OCI API-key profile and opens the Streamlit Bastion tunnel.

.DESCRIPTION
Creates or validates a named profile in the local Windows OCI configuration
file using explicit parameters or a validated Name=Value parameter file. It
then delegates Bastion session creation and SSH forwarding to the shared
connect-streamlit-bastion.ps1 launcher. Explicit parameters override file
values.

.EXAMPLE
.\connect-streamlit-api-key-auth.ps1 -ParameterFile .\output_assets.txt
#>

[CmdletBinding()]
param(
    [string]$ParameterFile,

    [ValidatePattern('^ocid1\.bastion\.')]
    [string]$BastionId,

    [ValidatePattern('^ocid1\.instance\.')]
    [string]$InstanceId,

    [string]$PrivateIp,

    [ValidatePattern('^[a-z]{2}-[a-z0-9-]+-[0-9]+$')]
    [string]$Region,

    [string]$SshPrivateKeyPath,

    [string]$SshPublicKeyPath,

    [ValidatePattern('^ocid1\.user\.')]
    [string]$OciUserId,

    [ValidatePattern('^ocid1\.tenancy\.')]
    [string]$OciTenancyId,

    [ValidatePattern('^([0-9A-Fa-f]{2}:){15}[0-9A-Fa-f]{2}$')]
    [string]$ApiKeyFingerprint,

    [string]$ApiPrivateKeyPath,

    [ValidatePattern('^[A-Za-z0-9_-]+$')]
    [string]$ProfileName = 'STREAMLIT_API_KEY',

    [string]$OciConfigFilePath,

    [ValidateRange(30, 10800)]
    [int]$BastionSessionTtl = 3600,

    [ValidateRange(1, 65535)]
    [int]$SshLocalPort = 22,

    [ValidateRange(0, 65535)]
    [int]$OptionalPort1 = 0,

    [ValidateRange(0, 65535)]
    [int]$OptionalPort2 = 0,

    [ValidateRange(0, 65535)]
    [int]$LocalPort = 0,

    [ValidateRange(0, 65535)]
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

$explicitParameters = @{}
foreach ($parameterName in $PSBoundParameters.Keys) {
    $explicitParameters[$parameterName] = $true
}

function Expand-ParameterFilePath {
    param(
        [Parameter(Mandatory = $true)][string]$Value,
        [Parameter(Mandatory = $true)][string]$BaseDirectory
    )

    $expanded = [Environment]::ExpandEnvironmentVariables($Value.Trim().Trim('"', "'"))
    $userHome = if ($env:USERPROFILE) { $env:USERPROFILE } else { $HOME }
    if ($expanded -match '^\$\{HOME\}(.*)$') {
        $expanded = $userHome + $Matches[1]
    }
    elseif ($expanded -match '^\$HOME(.*)$') {
        $expanded = $userHome + $Matches[1]
    }
    elseif ($expanded -eq '~') {
        $expanded = $userHome
    }
    elseif ($expanded.StartsWith('~\') -or $expanded.StartsWith('~/')) {
        $expanded = Join-Path $userHome $expanded.Substring(2)
    }
    if (-not [System.IO.Path]::IsPathRooted($expanded)) {
        $expanded = Join-Path $BaseDirectory $expanded
    }
    return $expanded
}

function Get-EffectiveString {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [AllowNull()][AllowEmptyString()][string]$CurrentValue,
        [Parameter(Mandatory = $true)][hashtable]$FileValues,
        [Parameter(Mandatory = $true)][hashtable]$ExplicitValues,
        [string]$FileDirectory,
        [switch]$IsPath
    )

    if (-not $ExplicitValues.ContainsKey($Name) -and $FileValues.ContainsKey($Name)) {
        $value = [string]$FileValues[$Name]
        if ($IsPath -and $value) {
            return Expand-ParameterFilePath -Value $value -BaseDirectory $FileDirectory
        }
        return $value
    }
    return $CurrentValue
}

function Get-EffectiveInteger {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][int]$CurrentValue,
        [Parameter(Mandatory = $true)][hashtable]$FileValues,
        [Parameter(Mandatory = $true)][hashtable]$ExplicitValues,
        [Parameter(Mandatory = $true)][int]$Minimum,
        [Parameter(Mandatory = $true)][int]$Maximum
    )

    $value = $CurrentValue
    if (-not $ExplicitValues.ContainsKey($Name) -and $FileValues.ContainsKey($Name)) {
        if (-not [int]::TryParse($FileValues[$Name], [ref]$value)) {
            throw "Parameter file value '$Name' must be an integer."
        }
    }
    if ($value -lt $Minimum -or $value -gt $Maximum) {
        throw "Parameter '$Name' must be from $Minimum through $Maximum."
    }
    return $value
}

function Get-EffectiveBoolean {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][bool]$CurrentValue,
        [Parameter(Mandatory = $true)][hashtable]$FileValues,
        [Parameter(Mandatory = $true)][hashtable]$ExplicitValues
    )

    $value = $CurrentValue
    if (-not $ExplicitValues.ContainsKey($Name) -and $FileValues.ContainsKey($Name)) {
        if (-not [bool]::TryParse($FileValues[$Name], [ref]$value)) {
            throw "Parameter file value '$Name' must be true or false."
        }
    }
    return $value
}

$parameterFileValues = @{}
$parameterFileDirectory = (Get-Location).Path
if ($ParameterFile) {
    $resolvedParameterFile = (Resolve-Path -LiteralPath $ParameterFile -ErrorAction Stop).Path
    if (-not (Test-Path -LiteralPath $resolvedParameterFile -PathType Leaf)) {
        throw "ParameterFile is not a file: $resolvedParameterFile"
    }
    $parameterFileDirectory = Split-Path -Parent $resolvedParameterFile
    $allowedKeys = @(
        'AssetsVersion', 'BastionId', 'InstanceId', 'PrivateIp', 'Region',
        'SshPrivateKeyPath', 'SshPublicKeyPath', 'OciUserId', 'OciTenancyId',
        'ApiKeyFingerprint', 'ApiPrivateKeyPath', 'ProfileName',
        'OciConfigFilePath', 'ConnectorScriptPath', 'BastionSessionTtl',
        'SshLocalPort', 'OptionalPort1', 'OptionalPort2',
        'LocalPort', 'RemotePort', 'WaitSeconds', 'PollSeconds', 'TargetUser',
        'OciExecutable', 'SshExecutable', 'ReplaceExistingProfile',
        'KeepSession', 'DryRun'
    )
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($resolvedParameterFile)) {
        $lineNumber++
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#') -or $trimmed.StartsWith(';')) { continue }
        $separator = $trimmed.IndexOf('=')
        if ($separator -le 0) {
            throw "Parameter file line $lineNumber must use Name=Value format."
        }
        $key = $trimmed.Substring(0, $separator).Trim()
        $value = $trimmed.Substring($separator + 1).Trim().Trim('"', "'")
        if ($allowedKeys -notcontains $key) {
            throw "Parameter file contains unsupported key '$key' on line $lineNumber."
        }
        if ($parameterFileValues.ContainsKey($key)) {
            throw "Parameter file contains duplicate key '$key'."
        }
        $parameterFileValues[$key] = $value
    }
    if ($parameterFileValues.ContainsKey('AssetsVersion') -and
        $parameterFileValues.AssetsVersion -ne '1') {
        throw "Unsupported parameter-file AssetsVersion '$($parameterFileValues.AssetsVersion)'."
    }
    Write-Host "Loaded API-key parameters from: $resolvedParameterFile"
}

$BastionId = Get-EffectiveString -Name 'BastionId' -CurrentValue $BastionId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$InstanceId = Get-EffectiveString -Name 'InstanceId' -CurrentValue $InstanceId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$PrivateIp = Get-EffectiveString -Name 'PrivateIp' -CurrentValue $PrivateIp -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$Region = Get-EffectiveString -Name 'Region' -CurrentValue $Region -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$SshPrivateKeyPath = Get-EffectiveString -Name 'SshPrivateKeyPath' -CurrentValue $SshPrivateKeyPath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$SshPublicKeyPath = Get-EffectiveString -Name 'SshPublicKeyPath' -CurrentValue $SshPublicKeyPath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$OciUserId = Get-EffectiveString -Name 'OciUserId' -CurrentValue $OciUserId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$OciTenancyId = Get-EffectiveString -Name 'OciTenancyId' -CurrentValue $OciTenancyId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$ApiKeyFingerprint = Get-EffectiveString -Name 'ApiKeyFingerprint' -CurrentValue $ApiKeyFingerprint -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$ApiPrivateKeyPath = Get-EffectiveString -Name 'ApiPrivateKeyPath' -CurrentValue $ApiPrivateKeyPath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$ProfileName = Get-EffectiveString -Name 'ProfileName' -CurrentValue $ProfileName -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$OciConfigFilePath = Get-EffectiveString -Name 'OciConfigFilePath' -CurrentValue $OciConfigFilePath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$TargetUser = Get-EffectiveString -Name 'TargetUser' -CurrentValue $TargetUser -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$OciExecutable = Get-EffectiveString -Name 'OciExecutable' -CurrentValue $OciExecutable -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$SshExecutable = Get-EffectiveString -Name 'SshExecutable' -CurrentValue $SshExecutable -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$BastionSessionTtl = Get-EffectiveInteger -Name 'BastionSessionTtl' -CurrentValue $BastionSessionTtl -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 30 -Maximum 10800
$SshLocalPort = Get-EffectiveInteger -Name 'SshLocalPort' -CurrentValue $SshLocalPort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 1 -Maximum 65535
$OptionalPort1 = Get-EffectiveInteger -Name 'OptionalPort1' -CurrentValue $OptionalPort1 -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$OptionalPort2 = Get-EffectiveInteger -Name 'OptionalPort2' -CurrentValue $OptionalPort2 -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$LocalPort = Get-EffectiveInteger -Name 'LocalPort' -CurrentValue $LocalPort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$RemotePort = Get-EffectiveInteger -Name 'RemotePort' -CurrentValue $RemotePort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$WaitSeconds = Get-EffectiveInteger -Name 'WaitSeconds' -CurrentValue $WaitSeconds -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 60 -Maximum 3600
$PollSeconds = Get-EffectiveInteger -Name 'PollSeconds' -CurrentValue $PollSeconds -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 1 -Maximum 60
$ReplaceExistingProfile = Get-EffectiveBoolean -Name 'ReplaceExistingProfile' -CurrentValue ([bool]$ReplaceExistingProfile) -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$KeepSession = Get-EffectiveBoolean -Name 'KeepSession' -CurrentValue ([bool]$KeepSession) -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$DryRun = Get-EffectiveBoolean -Name 'DryRun' -CurrentValue ([bool]$DryRun) -FileValues $parameterFileValues -ExplicitValues $explicitParameters

$requiredValues = @{
    BastionId = $BastionId
    InstanceId = $InstanceId
    PrivateIp = $PrivateIp
    Region = $Region
    SshPrivateKeyPath = $SshPrivateKeyPath
    OciUserId = $OciUserId
    OciTenancyId = $OciTenancyId
    ApiKeyFingerprint = $ApiKeyFingerprint
    ApiPrivateKeyPath = $ApiPrivateKeyPath
}
foreach ($requiredName in $requiredValues.Keys) {
    if ([string]::IsNullOrWhiteSpace([string]$requiredValues[$requiredName])) {
        throw "Missing required parameter '$requiredName'. Supply it directly or through -ParameterFile."
    }
}
if ($BastionId -notmatch '^ocid1\.bastion\.') { throw 'BastionId must be a Bastion OCID.' }
if ($InstanceId -notmatch '^ocid1\.instance\.') { throw 'InstanceId must be a Compute instance OCID.' }
$parsedPrivateIp = $null
if (-not [System.Net.IPAddress]::TryParse($PrivateIp, [ref]$parsedPrivateIp) -or
    $parsedPrivateIp.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork) {
    throw "PrivateIp must be a valid IPv4 address: $PrivateIp"
}
if ($Region -notmatch '^[a-z]{2}-[a-z0-9-]+-[0-9]+$') { throw "Invalid OCI region: $Region" }
if ($OciUserId -notmatch '^ocid1\.user\.') { throw 'OciUserId must be an OCI user OCID.' }
if ($OciTenancyId -notmatch '^ocid1\.tenancy\.') { throw 'OciTenancyId must be an OCI tenancy OCID.' }
if ($ApiKeyFingerprint -notmatch '^([0-9A-Fa-f]{2}:){15}[0-9A-Fa-f]{2}$') {
    throw 'ApiKeyFingerprint must contain 16 colon-separated hexadecimal bytes.'
}
if ($ProfileName -notmatch '^[A-Za-z0-9_-]+$') { throw "Invalid ProfileName: $ProfileName" }
if ($TargetUser -notmatch '^[a-z_][a-z0-9_-]*$') { throw "Invalid TargetUser: $TargetUser" }

if (-not $OciConfigFilePath) {
    $userHome = if ($env:USERPROFILE) { $env:USERPROFILE } else { $HOME }
    $OciConfigFilePath = Join-Path $userHome '.oci\config'
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
    SshLocalPort = $SshLocalPort
    OptionalPort1 = $OptionalPort1
    OptionalPort2 = $OptionalPort2
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
