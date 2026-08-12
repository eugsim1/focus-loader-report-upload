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

    [switch]$UseExistingSession,

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
    if ($expanded -match '^\$\{HOME\}(.*)$') { $expanded = $userHome + $Matches[1] }
    elseif ($expanded -match '^\$HOME(.*)$') { $expanded = $userHome + $Matches[1] }
    elseif ($expanded -eq '~') { $expanded = $userHome }
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
        'SshPrivateKeyPath', 'SshPublicKeyPath', 'ProfileName',
        'OciConfigFilePath', 'TenancyName', 'IdentityProviderName',
        'SessionExpirationMinutes', 'BastionSessionTtl', 'SshLocalPort',
        'OptionalPort1', 'OptionalPort2', 'LocalPort', 'RemotePort',
        'WaitSeconds', 'PollSeconds', 'TargetUser', 'OciExecutable',
        'SshExecutable', 'UseExistingSession', 'KeepSession', 'DryRun'
    )
    $lineNumber = 0
    foreach ($line in [System.IO.File]::ReadAllLines($resolvedParameterFile)) {
        $lineNumber++
        $trimmed = $line.Trim()
        if (-not $trimmed -or $trimmed.StartsWith('#') -or $trimmed.StartsWith(';')) { continue }
        $separator = $trimmed.IndexOf('=')
        if ($separator -le 0) { throw "Parameter file line $lineNumber must use Name=Value format." }
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
    Write-Host "Loaded browser-auth parameters from: $resolvedParameterFile"
}

$BastionId = Get-EffectiveString -Name 'BastionId' -CurrentValue $BastionId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$InstanceId = Get-EffectiveString -Name 'InstanceId' -CurrentValue $InstanceId -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$PrivateIp = Get-EffectiveString -Name 'PrivateIp' -CurrentValue $PrivateIp -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$Region = Get-EffectiveString -Name 'Region' -CurrentValue $Region -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$SshPrivateKeyPath = Get-EffectiveString -Name 'SshPrivateKeyPath' -CurrentValue $SshPrivateKeyPath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$SshPublicKeyPath = Get-EffectiveString -Name 'SshPublicKeyPath' -CurrentValue $SshPublicKeyPath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$ProfileName = Get-EffectiveString -Name 'ProfileName' -CurrentValue $ProfileName -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$OciConfigFilePath = Get-EffectiveString -Name 'OciConfigFilePath' -CurrentValue $OciConfigFilePath -FileValues $parameterFileValues -ExplicitValues $explicitParameters -FileDirectory $parameterFileDirectory -IsPath
$TenancyName = Get-EffectiveString -Name 'TenancyName' -CurrentValue $TenancyName -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$IdentityProviderName = Get-EffectiveString -Name 'IdentityProviderName' -CurrentValue $IdentityProviderName -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$TargetUser = Get-EffectiveString -Name 'TargetUser' -CurrentValue $TargetUser -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$OciExecutable = Get-EffectiveString -Name 'OciExecutable' -CurrentValue $OciExecutable -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$SshExecutable = Get-EffectiveString -Name 'SshExecutable' -CurrentValue $SshExecutable -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$SessionExpirationMinutes = Get-EffectiveInteger -Name 'SessionExpirationMinutes' -CurrentValue $SessionExpirationMinutes -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 5 -Maximum 60
$BastionSessionTtl = Get-EffectiveInteger -Name 'BastionSessionTtl' -CurrentValue $BastionSessionTtl -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 30 -Maximum 10800
$SshLocalPort = Get-EffectiveInteger -Name 'SshLocalPort' -CurrentValue $SshLocalPort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 1 -Maximum 65535
$OptionalPort1 = Get-EffectiveInteger -Name 'OptionalPort1' -CurrentValue $OptionalPort1 -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$OptionalPort2 = Get-EffectiveInteger -Name 'OptionalPort2' -CurrentValue $OptionalPort2 -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$LocalPort = Get-EffectiveInteger -Name 'LocalPort' -CurrentValue $LocalPort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$RemotePort = Get-EffectiveInteger -Name 'RemotePort' -CurrentValue $RemotePort -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 0 -Maximum 65535
$WaitSeconds = Get-EffectiveInteger -Name 'WaitSeconds' -CurrentValue $WaitSeconds -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 60 -Maximum 3600
$PollSeconds = Get-EffectiveInteger -Name 'PollSeconds' -CurrentValue $PollSeconds -FileValues $parameterFileValues -ExplicitValues $explicitParameters -Minimum 1 -Maximum 60
$UseExistingSession = Get-EffectiveBoolean -Name 'UseExistingSession' -CurrentValue ([bool]$UseExistingSession) -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$KeepSession = Get-EffectiveBoolean -Name 'KeepSession' -CurrentValue ([bool]$KeepSession) -FileValues $parameterFileValues -ExplicitValues $explicitParameters
$DryRun = Get-EffectiveBoolean -Name 'DryRun' -CurrentValue ([bool]$DryRun) -FileValues $parameterFileValues -ExplicitValues $explicitParameters

$requiredValues = @{
    BastionId = $BastionId; InstanceId = $InstanceId; PrivateIp = $PrivateIp
    Region = $Region; SshPrivateKeyPath = $SshPrivateKeyPath
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
if ($ProfileName -notmatch '^[A-Za-z0-9_-]+$') { throw "Invalid ProfileName: $ProfileName" }
if ($TargetUser -notmatch '^[a-z_][a-z0-9_-]*$') { throw "Invalid TargetUser: $TargetUser" }

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
