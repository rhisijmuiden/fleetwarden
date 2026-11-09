//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// securityScript reads BitLocker, Defender, Security Center, the firewall,
// the local administrators, Secure Boot and the TPM in one PowerShell run.
// Every part has its own try, so one missing feature (BitLocker on a Server
// Core without the feature, Security Center on Windows Server) does not hide
// the rest. Dates are written as ISO 8601 in UTC; PowerShell 5.1 would write
// them as "/Date(...)/" otherwise.
const securityScript = `$ErrorActionPreference='Stop'
function D($x){ if($x -and ([datetime]$x).Year -gt 2000){ ([datetime]$x).ToUniversalTime().ToString('o') } else { $null } }
$r=[ordered]@{ errors=@() }
try {
  $r.volumes=@(Get-BitLockerVolume | Where-Object { $_.VolumeType -ne 'Removable' } | ForEach-Object {
    [pscustomobject]@{ mount=[string]$_.MountPoint; os=([string]$_.VolumeType -eq 'OperatingSystem'); protection=[string]$_.ProtectionStatus
      status=[string]$_.VolumeStatus; percent=[int]$_.EncryptionPercentage; cipher=[string]$_.EncryptionMethod
      protectors=@($_.KeyProtector | ForEach-Object { [string]$_.KeyProtectorType })
      keys=@($_.KeyProtector | Where-Object { [string]$_.KeyProtectorType -eq 'RecoveryPassword' } | ForEach-Object { [pscustomobject]@{ id=[string]$_.KeyProtectorId; password=[string]$_.RecoveryPassword } }) } })
} catch { $r.errors+=('BitLocker: '+$_.Exception.Message) }
try {
  $mp=Get-MpComputerStatus
  $r.defender=[pscustomobject]@{ mode=[string]$mp.AMRunningMode; service=[bool]$mp.AMServiceEnabled; av=[bool]$mp.AntivirusEnabled
    rt=[bool]$mp.RealTimeProtectionEnabled; tamper=[bool]$mp.IsTamperProtected; sig=[string]$mp.AntivirusSignatureVersion
    sigAt=(D $mp.AntivirusSignatureLastUpdated); engine=[string]$mp.AMEngineVersion; quick=(D $mp.QuickScanEndTime); full=(D $mp.FullScanEndTime) }
  $names=@{}; foreach($t in @(Get-MpThreat -ErrorAction SilentlyContinue)){ $names[[string]$t.ThreatID]=$t }
  $since=(Get-Date).AddDays(-30)
  $r.threats=@(Get-MpThreatDetection -ErrorAction SilentlyContinue | Where-Object { $_.InitialDetectionTime -gt $since } |
    Sort-Object InitialDetectionTime -Descending | Select-Object -First 50 | ForEach-Object {
      $t=$names[[string]$_.ThreatID]
      [pscustomobject]@{ name=[string]$t.ThreatName; severity=[int]$t.SeverityID; at=(D $_.InitialDetectionTime); status=[int]$_.ThreatStatusID
        active=[bool]$t.IsActive; resources=@($_.Resources | Select-Object -First 5 | ForEach-Object { [string]$_ }) } })
} catch { $r.errors+=('Defender: '+$_.Exception.Message) }
try { $r.av=@(Get-CimInstance -Namespace root/SecurityCenter2 -ClassName AntiVirusProduct | ForEach-Object { [pscustomobject]@{ name=[string]$_.displayName; state=[uint32]$_.productState } }) } catch { }
try {
  $active=@(Get-NetConnectionProfile -ErrorAction SilentlyContinue | ForEach-Object { [string]$_.NetworkCategory })
  $r.firewall=@(Get-NetFirewallProfile -PolicyStore ActiveStore | ForEach-Object {
    $n=[string]$_.Name
    [pscustomobject]@{ name=$n; enabled=([string]$_.Enabled -eq 'True'); inbound=[string]$_.DefaultInboundAction
      active=(($active -contains $n) -or ($n -eq 'Domain' -and $active -contains 'DomainAuthenticated')) } })
} catch { $r.errors+=('Firewall: '+$_.Exception.Message) }
try {
  $name=(New-Object Security.Principal.SecurityIdentifier 'S-1-5-32-544').Translate([Security.Principal.NTAccount]).Value.Split('\')[-1]
  $grp=[ADSI]("WinNT://$env:COMPUTERNAME/$name,group")
  $r.admins=@(foreach($m in @($grp.psbase.Invoke('Members'))){
    $t=$m.GetType(); $sid=$null; $cls=''
    try { $sid=(New-Object Security.Principal.SecurityIdentifier([byte[]]$t.InvokeMember('objectSid','GetProperty',$null,$m,$null),0)).Value } catch { }
    try { $cls=[string]$t.InvokeMember('Class','GetProperty',$null,$m,$null) } catch { }
    [pscustomobject]@{ path=[string]$t.InvokeMember('ADsPath','GetProperty',$null,$m,$null); sid=$sid; cls=$cls } })
} catch { $r.errors+=('Administrators: '+$_.Exception.Message) }
try { $r.secureBoot=[bool](Confirm-SecureBootUEFI) } catch [System.PlatformNotSupportedException] { $r.secureBoot=$false } catch { }
try {
  $tpm=Get-CimInstance -Namespace root/cimv2/Security/MicrosoftTpm -ClassName Win32_Tpm
  if($tpm){ $r.tpm=[pscustomobject]@{ present=$true; ready=([bool]$tpm.IsEnabled_InitialValue -and [bool]$tpm.IsActivated_InitialValue); version=([string]$tpm.SpecVersion).Split(',')[0].Trim() } }
  else { $r.tpm=[pscustomobject]@{ present=$false; ready=$false; version='' } }
} catch { }
[pscustomobject]$r | ConvertTo-Json -Depth 6 -Compress`

func collectSecurity(ctx context.Context) (agentproto.SecurityReport, error) {
	out, err := runPowerShell(ctx, securityScript)
	if err != nil && len(out) == 0 {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return agentproto.SecurityReport{}, fmt.Errorf("security status: %v: %s", err, truncate(string(ee.Stderr), 300))
		}
		return agentproto.SecurityReport{}, fmt.Errorf("security status: %w", err)
	}
	return parseWinSecurity(out)
}

// securityActionScript is the PowerShell for one action.
func securityActionScript(act agentproto.SecurityAction) string {
	switch act.Action {
	case agentproto.SecurityQuickScan:
		return `$ErrorActionPreference='Stop'; Start-MpScan -ScanType QuickScan; 'Quick scan finished'`
	case agentproto.SecurityFullScan:
		return `$ErrorActionPreference='Stop'; Start-MpScan -ScanType FullScan; 'Full scan finished'`
	case agentproto.SecuritySignatures:
		return `$ErrorActionPreference='Stop'; Update-MpSignature; 'Signatures ' + (Get-MpComputerStatus).AntivirusSignatureVersion`
	case agentproto.SecurityRotateKey:
		// Add the new recovery password first, then remove the old ones, so
		// the volume always has one. Domain and Entra ID backups are a
		// bonus: the key reaches FleetWarden with the next report.
		return `$ErrorActionPreference='Stop'
$m='` + act.Mount + `'
$v=Get-BitLockerVolume -MountPoint $m
if([string]$v.VolumeStatus -eq 'FullyDecrypted'){ throw "$m is not encrypted with BitLocker" }
$old=@($v.KeyProtector | Where-Object { [string]$_.KeyProtectorType -eq 'RecoveryPassword' })
$null=Add-BitLockerKeyProtector -MountPoint $m -RecoveryPasswordProtector -WarningAction SilentlyContinue
$new=@((Get-BitLockerVolume -MountPoint $m).KeyProtector | Where-Object { [string]$_.KeyProtectorType -eq 'RecoveryPassword' -and ($old.KeyProtectorId -notcontains $_.KeyProtectorId) })[0]
foreach($p in $old){ $null=Remove-BitLockerKeyProtector -MountPoint $m -KeyProtectorId $p.KeyProtectorId }
try { if((Get-CimInstance Win32_ComputerSystem).PartOfDomain){ $null=Backup-BitLockerKeyProtector -MountPoint $m -KeyProtectorId $new.KeyProtectorId } } catch { }
try { $null=BackupToAAD-BitLockerKeyProtector -MountPoint $m -KeyProtectorId $new.KeyProtectorId } catch { }
'New recovery key ' + $new.KeyProtectorId`
	}
	return ""
}

func runSecurityAction(ctx context.Context, act agentproto.SecurityAction) (string, error) {
	script := securityActionScript(act)
	if script == "" {
		return "", fmt.Errorf("unknown action %s", act.Action)
	}
	exe := filepath.Join(os.Getenv("SystemRoot"), `System32\WindowsPowerShell\v1.0\powershell.exe`)
	cmd := exec.CommandContext(ctx, exe, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, truncate(string(out), 400))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), nil
}
