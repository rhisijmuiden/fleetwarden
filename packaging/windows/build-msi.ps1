# Builds the FleetWarden agent MSI (WiX Toolset v5). Runs on Windows, in CI on
# windows-latest:
#   ./packaging/windows/build-msi.ps1 -Version 0.1.0-test.5 -AgentExe dist/windows-amd64/fleetwarden-agent.exe -OutDir dist/agent
# Needs the .NET SDK; installs the wix tool and its extensions when missing.
param(
    [Parameter(Mandatory)] [string] $Version,
    [Parameter(Mandatory)] [string] $AgentExe,
    [string] $OutDir = "dist/agent",
    [string] $WixVersion = "5.0.2"
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# MSI versions are numeric: 0.1.0 -> 0.1.0.0, 0.1.0-test.5 -> 0.1.0.5.
if ($Version -notmatch '^(\d+)\.(\d+)\.(\d+)(?:-[a-z]+\.(\d+))?$') { throw "unsupported version '$Version'" }
$build = if ($Matches[4]) { $Matches[4] } else { '0' }
$msiVersion = "$($Matches[1]).$($Matches[2]).$($Matches[3]).$build"

if (-not (Get-Command wix -ErrorAction SilentlyContinue)) {
    dotnet tool install --global wix --version $WixVersion
    if ($LASTEXITCODE) { throw "installing wix failed" }
    $env:PATH = "$env:PATH;$env:USERPROFILE\.dotnet\tools"
}
foreach ($ext in 'WixToolset.Util.wixext', 'WixToolset.UI.wixext') {
    wix extension add --global "$ext/$WixVersion"
    if ($LASTEXITCODE) { throw "adding $ext failed" }
}

$here = $PSScriptRoot
dotnet build "$here/CustomActions/FleetWardenCA.csproj" -c Release --nologo
if ($LASTEXITCODE) { throw "building the custom actions failed" }
dotnet build "$here/Tray/FleetWardenTray.csproj" -c Release --nologo -p:Version=$msiVersion
if ($LASTEXITCODE) { throw "building the tray app failed" }
$tray = Join-Path $here 'Tray/bin/Release/net472/FleetWardenTray.exe'
if (-not (Test-Path $tray)) { throw "FleetWardenTray.exe was not produced" }
$ca = Get-ChildItem -Recurse "$here/CustomActions/bin/Release" -Filter FleetWardenCA.CA.dll | Select-Object -First 1
if (-not $ca) { throw "FleetWardenCA.CA.dll was not produced" }

New-Item -ItemType Directory -Force $OutDir | Out-Null
$out = Join-Path $OutDir "fleetwarden-agent_${Version}_x64.msi"
wix build -arch x64 -ext WixToolset.Util.wixext -ext WixToolset.UI.wixext `
    -d "Version=$msiVersion" -d "AgentExe=$((Resolve-Path $AgentExe).Path)" -d "CADll=$($ca.FullName)" -d "TrayExe=$((Resolve-Path $tray).Path)" `
    -o $out "$here/FleetWardenAgent.wxs"
if ($LASTEXITCODE) { throw "wix build failed" }
Remove-Item -ErrorAction SilentlyContinue ([IO.Path]::ChangeExtension($out, '.wixpdb'))
Write-Host "Built $out (MSI version $msiVersion)"
