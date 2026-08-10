<#
.SYNOPSIS
Creates an OCI Bastion managed-SSH session and opens a local Streamlit tunnel.

.DESCRIPTION
The script reuses an existing OCI Bastion service, creates a time-limited
managed-SSH session to an existing private Compute instance, waits for the
session to become ACTIVE, and runs OpenSSH in the foreground with a loopback-
only local forward. Press Ctrl+C to close the tunnel. By default, the newly
created Bastion session is deleted when SSH exits.

.EXAMPLE
.\connect-streamlit-bastion.ps1 `
  -BastionId 'ocid1.bastion.oc1.eu-frankfurt-1.example' `
  -InstanceId 'ocid1.instance.oc1.eu-frankfurt-1.example' `
  -PrivateIp '10.30.1.10' `
  -Region 'eu-frankfurt-1' `
  -SshPrivateKeyPath "$HOME\.ssh\bastion_ed25519"
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
    [ValidatePattern('^[0-9]{1,3}(\.[0-9]{1,3}){3}$')]
    [string]$PrivateIp,

    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[a-z]{2}-[a-z0-9-]+-[0-9]+$')]
    [string]$Region,

    [Parameter(Mandatory = $true)]
    [string]$SshPrivateKeyPath,

    [string]$SshPublicKeyPath,

    [string]$Profile = 'DEFAULT',

    [string]$AuthMode,

    [string]$OciConfigFilePath,

    [ValidatePattern('^[a-z_][a-z0-9_-]*$')]
    [string]$TargetUser = 'oracle',

    [ValidateRange(30, 10800)]
    [int]$SessionTtl = 10800,

    [ValidateRange(1, 65535)]
    [int]$LocalPort = 8501,

    [ValidateRange(1, 65535)]
    [int]$RemotePort = 8501,

    [ValidateRange(60, 3600)]
    [int]$WaitSeconds = 1200,

    [ValidateRange(1, 60)]
    [int]$PollSeconds = 10,

    [string]$SessionDisplayName,

    [string]$OciExecutable = 'oci',

    [string]$SshExecutable = 'ssh',

    [switch]$KeepSession,

    [switch]$DryRun
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

[System.Net.IPAddress]$parsedPrivateIp = $null
if (-not [System.Net.IPAddress]::TryParse($PrivateIp, [ref]$parsedPrivateIp) -or
    $parsedPrivateIp.AddressFamily -ne [System.Net.Sockets.AddressFamily]::InterNetwork) {
    throw "PrivateIp must be a valid IPv4 address: $PrivateIp"
}

function Resolve-ExecutablePath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $command = Get-Command -Name $Name -CommandType Application -ErrorAction Stop |
        Select-Object -First 1
    if ($command.Path) {
        return $command.Path
    }
    return $command.Source
}

function Invoke-NativeText {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Executable,

        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    Write-Verbose ("Running: {0} {1}" -f $Executable, ($Arguments -join ' '))

    # Windows PowerShell 5.1 represents each native stderr line as an
    # ErrorRecord. Merge the native streams temporarily, classify the records,
    # and reconstruct stdout/stderr without PowerShell's location metadata.
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
        if (-not $details) {
            $details = $stdout
        }
        if (-not $details) {
            $details = 'The command returned no diagnostic output.'
        }
        throw "Command failed with exit code ${exitCode}: $Executable $($Arguments -join ' ')`n$details"
    }
    if ($stderr) {
        Write-Warning $stderr
    }
    return $stdout
}

function Invoke-OciText {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    return Invoke-NativeText -Executable $script:ResolvedOciExecutable `
        -Arguments ($Arguments + $script:OciGlobalArguments)
}

function Invoke-OciJson {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments
    )

    $jsonText = Invoke-OciText -Arguments ($Arguments + @('--output', 'json'))
    try {
        return $jsonText | ConvertFrom-Json -ErrorAction Stop
    }
    catch {
        throw "OCI CLI returned invalid JSON for: oci $($Arguments -join ' ')`n$($_.Exception.Message)"
    }
}

function Test-LocalPortInUse {
    param(
        [Parameter(Mandatory = $true)]
        [int]$Port
    )

    $listeners = [System.Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners()
    return ($listeners | Where-Object { $_.Port -eq $Port } | Select-Object -First 1) -ne $null
}

$privateKey = (Resolve-Path -LiteralPath $SshPrivateKeyPath -ErrorAction Stop).Path
if (-not $SshPublicKeyPath) {
    $SshPublicKeyPath = "${privateKey}.pub"
}
$publicKey = (Resolve-Path -LiteralPath $SshPublicKeyPath -ErrorAction Stop).Path
if (-not (Test-Path -LiteralPath $privateKey -PathType Leaf)) {
    throw "SshPrivateKeyPath is not a file: $privateKey"
}
if (-not (Test-Path -LiteralPath $publicKey -PathType Leaf)) {
    throw "SshPublicKeyPath is not a file: $publicKey"
}

if (-not $SessionDisplayName) {
    $timestamp = (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ')
    $SessionDisplayName = "streamlit-$($env:USERNAME)-$timestamp-$PID"
}
if ($SessionDisplayName.Length -gt 255) {
    throw 'SessionDisplayName cannot exceed 255 characters.'
}
if ($SessionDisplayName -match '[\x00-\x1f\x7f]') {
    throw 'SessionDisplayName cannot contain control characters.'
}
if (Test-LocalPortInUse -Port $LocalPort) {
    throw "Local TCP port $LocalPort is already in use. Choose another value with -LocalPort."
}

$browserUrl = "http://127.0.0.1:${LocalPort}/"
if ($DryRun) {
    Write-Host 'DRY RUN: no Bastion session or SSH process will be created.'
    Write-Host "Bastion:      $BastionId"
    Write-Host "Compute:      $InstanceId"
    Write-Host "Target:       ${TargetUser}@${PrivateIp}:22"
    Write-Host "Public key:   $publicKey"
    Write-Host "Private key:  $privateKey"
    if ($OciConfigFilePath) {
        Write-Host "OCI config:   $OciConfigFilePath"
    }
    Write-Host "Local tunnel: 127.0.0.1:${LocalPort} -> 127.0.0.1:${RemotePort}"
    Write-Host "Browser URL:  $browserUrl"
    return
}

$script:ResolvedOciExecutable = Resolve-ExecutablePath -Name $OciExecutable
$resolvedSshExecutable = Resolve-ExecutablePath -Name $SshExecutable
$script:OciGlobalArguments = @('--profile', $Profile, '--region', $Region)
if ($AuthMode) {
    $script:OciGlobalArguments += @('--auth', $AuthMode)
}
if ($OciConfigFilePath) {
    $resolvedOciConfigFile = (Resolve-Path -LiteralPath $OciConfigFilePath -ErrorAction Stop).Path
    $script:OciGlobalArguments += @('--config-file', $resolvedOciConfigFile)
}

Write-Host "Checking existing Bastion service in $Region..."
$bastionDetails = Invoke-OciJson -Arguments @(
    'bastion', 'bastion', 'get',
    '--bastion-id', $BastionId
)
$bastionState = [string]$bastionDetails.data.'lifecycle-state'
if (-not $bastionState) {
    throw "OCI returned no lifecycle state for Bastion $BastionId."
}
if ($bastionState -ne 'ACTIVE') {
    throw "Bastion $BastionId is $bastionState; expected ACTIVE."
}

$sessionId = $null
$sessionCreated = $false
try {
    Write-Host "Creating managed-SSH session $SessionDisplayName..."
    [void](Invoke-OciText -Arguments @(
        'bastion', 'session', 'create-managed-ssh',
        '--bastion-id', $BastionId,
        '--display-name', $SessionDisplayName,
        '--key-type', 'PUB',
        '--session-ttl', $SessionTtl.ToString(),
        '--ssh-public-key-file', $publicKey,
        '--target-os-username', $TargetUser,
        '--target-port', '22',
        '--target-private-ip', $PrivateIp,
        '--target-resource-id', $InstanceId,
        '--wait-for-state', 'SUCCEEDED',
        '--max-wait-seconds', $WaitSeconds.ToString(),
        '--wait-interval-seconds', $PollSeconds.ToString()
    ))
    $sessionCreated = $true

    $discoveryTimer = [System.Diagnostics.Stopwatch]::StartNew()
    $discoveryIteration = 0
    while ($discoveryTimer.Elapsed.TotalSeconds -lt $WaitSeconds) {
        $discoveryIteration++
        $sessionList = Invoke-OciJson -Arguments @(
            'bastion', 'session', 'list',
            '--bastion-id', $BastionId,
            '--display-name', $SessionDisplayName,
            '--all'
        )
        $sessions = @($sessionList.data)
        $sessionId = $null
        if ($sessions.Count -gt 0) {
            $sessionId = [string]$sessions[0].id
        }
        Write-Host "Session discovery check ${discoveryIteration}: $sessionId"
        if ($sessionId -match '^ocid1\.bastionsession\.') {
            break
        }
        $sessionId = $null
        Start-Sleep -Seconds $PollSeconds
    }
    if (-not $sessionId) {
        throw "The Bastion work request succeeded, but session $SessionDisplayName was not discoverable within $WaitSeconds seconds."
    }

    $activeTimer = [System.Diagnostics.Stopwatch]::StartNew()
    $activeIteration = 0
    $sessionState = 'UNKNOWN'
    while ($activeTimer.Elapsed.TotalSeconds -lt $WaitSeconds) {
        $activeIteration++
        $sessionDetails = Invoke-OciJson -Arguments @(
            'bastion', 'session', 'get',
            '--session-id', $sessionId
        )
        $sessionState = [string]$sessionDetails.data.'lifecycle-state'
        if (-not $sessionState) {
            $sessionState = 'UNKNOWN'
        }
        Write-Host "Bastion session check ${activeIteration}: status=$sessionState; expected=ACTIVE"
        if ($sessionState -eq 'ACTIVE') {
            break
        }
        if ($sessionState -in @('FAILED', 'DELETED', 'DELETING')) {
            throw "Bastion session entered terminal state $sessionState."
        }
        Start-Sleep -Seconds $PollSeconds
    }
    if ($sessionState -ne 'ACTIVE') {
        throw "Bastion session did not reach ACTIVE within $WaitSeconds seconds; final state: $sessionState"
    }

    $bastionHost = "host.bastion.$Region.oci.oraclecloud.com"
    $proxyCommand = "ssh -i `"$privateKey`" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -W %h:%p -p 22 ${sessionId}@${bastionHost}"
    $forwardSpec = "127.0.0.1:${LocalPort}:127.0.0.1:${RemotePort}"
    $sshArguments = @(
        '-N',
        '-L', $forwardSpec,
        '-i', $privateKey,
        '-o', 'IdentitiesOnly=yes',
        '-o', 'StrictHostKeyChecking=accept-new',
        '-o', 'ExitOnForwardFailure=yes',
        '-o', 'ServerAliveInterval=120',
        '-o', 'ServerAliveCountMax=3',
        '-o', "ProxyCommand=$proxyCommand",
        '-p', '22',
        "${TargetUser}@${PrivateIp}"
    )

    Write-Host "Bastion session is ACTIVE: $sessionId"
    Write-Host "Starting loopback-only tunnel: $forwardSpec"
    Write-Host "Keep this PowerShell window open and browse to $browserUrl"
    Write-Host 'Press Ctrl+C to close the tunnel.'

    & $resolvedSshExecutable @sshArguments
    $sshExitCode = $LASTEXITCODE
    if ($sshExitCode -ne 0) {
        throw "OpenSSH exited with code $sshExitCode."
    }
}
finally {
    if ($sessionCreated -and $sessionId -and -not $KeepSession) {
        Write-Host "Deleting temporary Bastion session $sessionId..."
        try {
            [void](Invoke-OciText -Arguments @(
                'bastion', 'session', 'delete',
                '--session-id', $sessionId,
                '--force'
            ))
        }
        catch {
            Write-Warning "Could not delete Bastion session $sessionId automatically: $($_.Exception.Message)"
        }
    }
    elseif ($sessionId -and $KeepSession) {
        Write-Host "Keeping Bastion session until its TTL expires: $sessionId"
    }
}
