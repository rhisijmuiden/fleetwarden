# Installs the agent MSI silently, checks the result and uninstalls it again.
# CI runs it on windows-latest:  ./packaging/windows/test-msi.ps1 -Msi dist/agent/fleetwarden-agent_..._x64.msi
param([Parameter(Mandatory)] [string] $Msi)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$msi = (Resolve-Path $Msi).Path
$exe = Join-Path $env:ProgramFiles 'FleetWarden Agent\fleetwarden-agent.exe'
$config = Join-Path $env:ProgramData 'FleetWarden\agent.json'
$token = 'fwe_ci' + ('x' * 39)

function Invoke-Msi([string] $arguments, [string] $log) {
    $p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList "$arguments /qn /norestart /l*v `"$log`""
    if ($p.ExitCode -notin 0, 3010) {
        Get-Content $log -Tail 80
        throw "msiexec $arguments failed with exit code $($p.ExitCode)"
    }
}

# Install with enrollment settings, as the Add client one-liner does.
Invoke-Msi "/i `"$msi`" SERVER=http://127.0.0.1:9 TOKEN=$token GROUP=CI" "$env:RUNNER_TEMP\install.log"
if (-not (Test-Path $exe)) { throw "agent executable missing" }
$svc = Get-CimInstance Win32_Service -Filter "Name='FleetWardenAgent'"
if (-not $svc) { throw "service FleetWardenAgent not installed" }
if ($svc.StartMode -ne 'Auto' -or $svc.StartName -ne 'LocalSystem') { throw "service has start mode $($svc.StartMode) as $($svc.StartName)" }
$status = & $exe status
Write-Host "status: $status"
if ($status -notmatch 'Waiting to enroll with http://127.0.0.1:9') { throw "agent was not configured by the installer" }
if (Select-String -Path "$env:RUNNER_TEMP\install.log" -SimpleMatch $token -Quiet) { throw "the token appears in the install log" }
$acl = (Get-Acl $config).Access | Where-Object { $_.IdentityReference -match 'Users|Everyone' }
if ($acl) { throw "agent config is readable by $($acl.IdentityReference -join ', ')" }
if (-not (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Services\EventLog\Application\FleetWardenAgent' -ErrorAction SilentlyContinue)) { throw "event log source missing" }

# The inventory collector must work on a real Windows machine.
$inv = & $exe inventory | ConvertFrom-Json
Write-Host "inventory: $($inv.hostname), $($inv.os.name) $($inv.os.version), $($inv.software.Count) programs"
if (-not $inv.hostname -or $inv.software.Count -lt 1) { throw "inventory looks empty" }

# Reinstall without settings (the update path) keeps the configuration.
Invoke-Msi "/i `"$msi`" REINSTALL=ALL REINSTALLMODE=vomus" "$env:RUNNER_TEMP\repair.log"
if ((& $exe status) -notmatch 'Waiting to enroll') { throw "repair lost the configuration" }

# Uninstall removes the service, the files and the local enrollment.
Invoke-Msi "/x `"$msi`"" "$env:RUNNER_TEMP\uninstall.log"
if (Get-Service FleetWardenAgent -ErrorAction SilentlyContinue) { throw "service still present after uninstall" }
if (Test-Path $exe) { throw "agent executable still present after uninstall" }
if (Test-Path $config) { throw "agent configuration still present after uninstall" }
Write-Host "MSI test passed"
