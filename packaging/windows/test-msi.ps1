# Installs the agent MSI silently, checks the result and uninstalls it again.
# CI runs it on windows-latest:  ./packaging/windows/test-msi.ps1 -Msi dist/agent/fleetwarden-agent_..._x64.msi
param(
    [Parameter(Mandatory)] [string] $Msi,
    # A newer build of the same MSI, for the self-update test (a major upgrade).
    [string] $NextMsi
)
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
$fwRules = @(Get-NetFirewallRule -DisplayName 'FleetWarden Agent (direct remote control*' -ErrorAction SilentlyContinue)
if ($fwRules.Count -ne 2) { throw "expected 2 firewall rules for the direct remote control path, found $($fwRules.Count)" }
foreach ($r in $fwRules) {
    $app = ($r | Get-NetFirewallApplicationFilter).Program
    $proto = ($r | Get-NetFirewallPortFilter).Protocol
    Write-Host "firewall rule: $($r.DisplayName) $($r.Direction) $($r.Profile) $proto $app"
    if ($r.Direction -ne 'Inbound' -or $proto -ne 'UDP' -or $app -ne $exe) { throw "firewall rule $($r.DisplayName) looks wrong" }
}

# The inventory collector must work on a real Windows machine.
$inv = & $exe inventory | ConvertFrom-Json
Write-Host "inventory: $($inv.hostname), $($inv.os.name) $($inv.os.version), $($inv.software.Count) programs"
if (-not $inv.hostname -or $inv.software.Count -lt 1) { throw "inventory looks empty" }

# The tray app: installed, started at logon, and able to render its pop-up
# (the images are kept as a CI artifact).
$tray = Join-Path $env:ProgramFiles 'FleetWarden Agent\FleetWardenTray.exe'
if (-not (Test-Path $tray)) { throw "tray app missing" }
$runKey = Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'
$run = if ($runKey.PSObject.Properties['FleetWardenTray']) { $runKey.FleetWardenTray } else { '' }
if ($run -ne "`"$tray`"") { throw "tray app Run entry is '$run'" }
$shots = Join-Path $env:RUNNER_TEMP 'tray'
New-Item -ItemType Directory -Force $shots | Out-Null
foreach ($demo in @('install', 'en', '--light'), @('reboot', 'nl', '--dark'), @('install', 'nl', '--light'), @('browser', 'en', '--light'), @('browser', 'nl', '--dark'), @('restart', 'en', '--light'), @('shutdown', 'nl', '--dark'), @('remote', 'en', '--light'), @('remote', 'nl', '--dark'), @('banner', 'en', '--light'), @('banner', 'nl', '--dark')) {
    $png = Join-Path $shots "popup-$($demo[0])-$($demo[1]).png"
    $p = Start-Process $tray -ArgumentList '--demo', $demo[0], '--lang', $demo[1], $demo[2], '--screenshot', "`"$png`"" -PassThru
    if (-not $p.WaitForExit(60000)) { $p.Kill(); throw "tray app hung rendering $($demo -join ' ')" }
    if ($p.ExitCode -ne 0 -or -not (Test-Path $png)) { throw "tray app could not render its pop-up ($($demo -join ' '), exit $($p.ExitCode))" }
    Write-Host "rendered $png ($((Get-Item $png).Length) bytes)"
}
# The chat window with example messages.
foreach ($demo in @('en', '--light'), @('nl', '--dark')) {
    $png = Join-Path $shots "chat-$($demo[0]).png"
    $p = Start-Process $tray -ArgumentList '--demo', 'chat', '--lang', $demo[0], $demo[1], '--screenshot', "`"$png`"" -PassThru
    if (-not $p.WaitForExit(60000)) { $p.Kill(); throw "tray app hung rendering the chat ($($demo -join ' '))" }
    if ($p.ExitCode -ne 0 -or -not (Test-Path $png)) { throw "tray app could not render the chat ($($demo -join ' '), exit $($p.ExitCode))" }
    Write-Host "rendered $png ($((Get-Item $png).Length) bytes)"
}
# The self-service window with example data, every page in both languages.
foreach ($demo in @('software', 'en', '--light'), @('software', 'nl', '--dark'), @('updates', 'en', '--light'), @('updates', 'nl', '--light'), @('help', 'en', '--dark'), @('help', 'nl', '--light')) {
    $png = Join-Path $shots "selfservice-$($demo[0])-$($demo[1]).png"
    $p = Start-Process $tray -ArgumentList '--demo', 'selfservice', '--page', $demo[0], '--lang', $demo[1], $demo[2], '--screenshot', "`"$png`"" -PassThru
    if (-not $p.WaitForExit(60000)) { $p.Kill(); throw "tray app hung rendering self-service $($demo -join ' ')" }
    if ($p.ExitCode -ne 0 -or -not (Test-Path $png)) { throw "tray app could not render its self-service window ($($demo -join ' '), exit $($p.ExitCode))" }
    Write-Host "rendered $png ($((Get-Item $png).Length) bytes)"
}

# The remote desktop helper the way a session starts it: the agent runs as
# SYSTEM and starts the helper in the console session. A warning only, as the
# runner's own session may not be the console session.
$remotePng = Join-Path $shots 'remote-system.png'
$taskOut = Join-Path $env:RUNNER_TEMP 'remote-test.txt'
$action = New-ScheduledTaskAction -Execute 'cmd.exe' -Argument "/c `"`"$exe`" remote-test --screenshot `"$remotePng`" > `"$taskOut`" 2>&1`""
Register-ScheduledTask -TaskName 'FleetWardenRemoteTest' -Action $action -User 'SYSTEM' -RunLevel Highest -Force | Out-Null
Start-ScheduledTask -TaskName 'FleetWardenRemoteTest'
$deadline = (Get-Date).AddSeconds(60)
while ((Get-ScheduledTask -TaskName 'FleetWardenRemoteTest').State -eq 'Running' -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 500 }
Unregister-ScheduledTask -TaskName 'FleetWardenRemoteTest' -Confirm:$false
if (Test-Path $taskOut) { Get-Content $taskOut | ForEach-Object { Write-Host "remote-test: $_" } }
if (Test-Path $remotePng) { Write-Host "remote helper captured $remotePng ($((Get-Item $remotePng).Length) bytes)" }
else { Write-Host "::warning::the remote desktop helper did not capture the console session" }

# A real Windows Update scan through WUA (the runner image disables the service).
Set-Service wuauserv -StartupType Manual -ErrorAction SilentlyContinue
$sw = [Diagnostics.Stopwatch]::StartNew()
$scan = (& $exe patch-scan) -join "`n" | ConvertFrom-Json
if ($LASTEXITCODE) { throw "patch-scan failed (exit $LASTEXITCODE)" }
Write-Host "patch scan: source $($scan.source), $(@($scan.updates).Count) updates, reboot pending $($scan.rebootPending), $([int]$sw.Elapsed.TotalSeconds)s"
foreach ($u in @($scan.updates) | Select-Object -First 10) { Write-Host "  [$($u.category)] $($u.title)" }
if ($scan.source -notin 'windows-update', 'wsus') { throw "unexpected update source '$($scan.source)'" }

# Reinstall without settings (the update path) keeps the configuration.
Invoke-Msi "/i `"$msi`" REINSTALL=ALL REINSTALLMODE=vomus" "$env:RUNNER_TEMP\repair.log"
if ((& $exe status) -notmatch 'Waiting to enroll') { throw "repair lost the configuration" }

if ($NextMsi) {
# A running tray app must not block the upgrade.
$trayProc = Start-Process $tray -PassThru
Start-Sleep -Seconds 2
if ($trayProc.HasExited) { throw "tray app exited right after starting (exit $($trayProc.ExitCode))" }
# Self-update helper: upgrade to the next MSI from an update plan, wait for the
# new version to report a connection, and write the result for the agent.
$state = Join-Path $env:ProgramData 'FleetWarden'
$upd = Join-Path $state 'update'
New-Item -ItemType Directory -Force $upd | Out-Null
Copy-Item (Resolve-Path $NextMsi).Path (Join-Path $upd 'fleetwarden-agent_9.9.9_x64.msi')
Copy-Item $exe (Join-Path $upd 'fleetwarden-agent-helper.exe')
$started = (Get-Date).ToUniversalTime()
@{ version = '9.9.9'; fromVersion = '0.0.1'; package = (Join-Path $upd 'fleetwarden-agent_9.9.9_x64.msi'); stateDir = $state; startedAt = $started.ToString('o') } |
    ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $upd 'plan.json')
Remove-Item (Join-Path $state 'update-result.json') -ErrorAction SilentlyContinue
$helper = Start-Process (Join-Path $upd 'fleetwarden-agent-helper.exe') -ArgumentList 'update-helper', '--plan', "`"$(Join-Path $upd 'plan.json')`"" -PassThru
Start-Sleep -Seconds 3
@{ version = '9.9.9'; at = (Get-Date).ToUniversalTime().ToString('o') } | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $state 'connected.json')
if (-not $helper.WaitForExit(240000)) { throw "update helper did not finish" }
$result = Get-Content (Join-Path $state 'update-result.json') -Raw | ConvertFrom-Json
$resultMsg = if ($result.PSObject.Properties.Name -contains 'message') { $result.message } else { '' } # omitted when empty
Write-Host "update helper: $($result.state) $resultMsg"
Get-Content (Join-Path $upd 'helper.log') -ErrorAction SilentlyContinue
if ($result.state -ne 'completed') {
    Get-Content (Join-Path $upd 'fleetwarden-agent_9.9.9_x64.msi.log') -Tail 60 -ErrorAction SilentlyContinue
    throw "update helper reported $($result.state): $resultMsg"
}
if (-not (Get-Service FleetWardenAgent -ErrorAction SilentlyContinue)) { throw "service missing after the update helper ran" }
if ((& $exe status) -notmatch 'Waiting to enroll') { throw "the upgrade lost the configuration" }
if (-not $trayProc.HasExited) { $trayProc.Kill(); throw "the upgrade did not close the running tray app" }
if (-not (Test-Path $tray)) { throw "tray app missing after the upgrade" }
}

# Uninstall removes the service, the files and the local enrollment.
$installed = if ($NextMsi) { (Resolve-Path $NextMsi).Path } else { $msi }
Invoke-Msi "/x `"$installed`"" "$env:RUNNER_TEMP\uninstall.log"
if (Get-Service FleetWardenAgent -ErrorAction SilentlyContinue) { throw "service still present after uninstall" }
if (Test-Path $exe) { throw "agent executable still present after uninstall" }
if (Test-Path $config) { throw "agent configuration still present after uninstall" }
if (Test-Path $tray) { throw "tray app still present after uninstall" }
if (Get-NetFirewallRule -DisplayName 'FleetWarden Agent (direct remote control*' -ErrorAction SilentlyContinue) { throw "firewall rules still present after uninstall" }
if ((Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -ErrorAction SilentlyContinue).PSObject.Properties['FleetWardenTray']) { throw "tray Run entry still present after uninstall" }
Write-Host "MSI test passed"
