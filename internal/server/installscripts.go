package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// safeArg limits what goes into generated scripts: URLs and tokens only.
var safeArg = regexp.MustCompile(`^[A-Za-z0-9:/._\-?=&%\[\]@]+$`)

// handleInstallPS1 serves the PowerShell one-liner target:
//
//	iwr 'https://em.example.com/agent/install.ps1?t=TOKEN' -UseBasicParsing | iex
func (s *Server) handleInstallPS1(w http.ResponseWriter, r *http.Request) {
	_, plain, ok := s.tokenFromQuery(w, r)
	if !ok {
		return
	}
	base := s.baseURL(r)
	if !safeArg.MatchString(base) || !safeArg.MatchString(plain) {
		http.Error(w, "invalid server address", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, installPS1, base, plain)
}

const installPS1 = `# FleetWarden agent installer. Run in an elevated PowerShell.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$server = '%s'
$token = '%s'
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this command in PowerShell as Administrator.'
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$msi = Join-Path $env:TEMP 'fleetwarden-agent.msi'
$log = Join-Path $env:TEMP 'fleetwarden-agent-install.log'
Write-Host "Downloading the FleetWarden agent from $server ..."
Invoke-WebRequest -Uri "$server/agent/download/windows-x64.msi?t=$token" -OutFile $msi -UseBasicParsing
Write-Host 'Installing ...'
$msiArgs = '/i "{0}" /qn /norestart /l*v "{1}" SERVER={2} TOKEN={3}' -f $msi, $log, $server, $token
$p = Start-Process msiexec.exe -Wait -PassThru -ArgumentList $msiArgs
Remove-Item $msi -ErrorAction SilentlyContinue
if ($p.ExitCode -ne 0 -and $p.ExitCode -ne 3010) {
    throw "Installation failed with exit code $($p.ExitCode). See $log"
}
Write-Host 'The FleetWarden agent is installed and connects to the server.'
`

// handleInstallSh serves the Linux one-liner target:
//
//	curl -fsSL https://em.example.com/agent/install.sh | sudo bash -s -- TOKEN
//
// The token is checked when the script downloads the package.
func (s *Server) handleInstallSh(w http.ResponseWriter, r *http.Request) {
	base := s.baseURL(r)
	if !safeArg.MatchString(base) {
		http.Error(w, "invalid server address", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, installSh, base)
}

const installSh = `#!/usr/bin/env bash
# FleetWarden agent installer for Debian, Ubuntu and Proxmox VE.
#   curl -fsSL <server>/agent/install.sh | sudo bash -s -- TOKEN
set -euo pipefail
SERVER='%s'
TOKEN="${1:-${FW_TOKEN:-}}"
die() { printf 'fleetwarden: %%s\n' "$*" >&2; exit 1; }
[[ -n "$TOKEN" ]] || die "usage: install.sh TOKEN"
[[ "$TOKEN" =~ ^[A-Za-z0-9_-]+$ ]] || die "invalid token"
[[ $EUID -eq 0 ]] || die "run as root (sudo)"
command -v dpkg >/dev/null || die "this installer needs a Debian-based system"
arch=$(dpkg --print-architecture)
case "$arch" in amd64|arm64) ;; *) die "unsupported architecture $arch" ;; esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading the FleetWarden agent from $SERVER ..."
if command -v curl >/dev/null; then
    curl -fsSL -o "$tmp/fleetwarden-agent.deb" "$SERVER/agent/download/linux-$arch.deb?t=$TOKEN"
else
    wget -qO "$tmp/fleetwarden-agent.deb" "$SERVER/agent/download/linux-$arch.deb?t=$TOKEN"
fi
chmod 0644 "$tmp/fleetwarden-agent.deb"
export DEBIAN_FRONTEND=noninteractive
apt-get install -y -q "$tmp/fleetwarden-agent.deb"
fleetwarden-agent status || true
`

// bakeMSI sets the SERVER and TOKEN properties in a copy of the agent MSI
// (msitools), so a double-click install needs no input. Without msitools the
// plain MSI is served and the wizard asks for both.
func (s *Server) bakeMSI(ctx context.Context, msi []byte, cfg enrollConfig) ([]byte, error) {
	if _, err := exec.LookPath("msibuild"); err != nil {
		return nil, errors.New("msitools is not installed")
	}
	if !safeArg.MatchString(cfg.Server) || !safeArg.MatchString(cfg.Token) {
		return nil, errors.New("unsafe property value")
	}
	dir, err := os.MkdirTemp(s.cfg.TmpDir(), "msi-")
	if err != nil {
		dir, err = os.MkdirTemp("", "fleetwarden-msi-")
		if err != nil {
			return nil, err
		}
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "agent.msi")
	if err := os.WriteFile(path, msi, 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "msiinfo", "export", path, "Property").Output()
	if err != nil {
		return nil, fmt.Errorf("msiinfo: %w", err)
	}
	idt := setIDTProperties(out, map[string]string{"SERVER": cfg.Server, "TOKEN": cfg.Token})
	if err := os.WriteFile(filepath.Join(dir, "Property.idt"), idt, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "msibuild", path, "-i", filepath.Join(dir, "Property.idt"))
	if msg, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("msibuild: %v: %s", err, msg)
	}
	return os.ReadFile(path)
}

// setIDTProperties replaces or adds rows in an exported Property table.
func setIDTProperties(idt []byte, props map[string]string) []byte {
	lines := strings.Split(strings.ReplaceAll(string(idt), "\r\n", "\n"), "\n")
	var out bytes.Buffer
	for i, l := range lines {
		if l == "" {
			continue
		}
		if i >= 3 {
			name, _, _ := strings.Cut(l, "\t")
			if _, ok := props[name]; ok {
				continue
			}
		}
		out.WriteString(l + "\r\n")
	}
	for _, k := range []string{"SERVER", "TOKEN"} {
		if v, ok := props[k]; ok {
			out.WriteString(k + "\t" + v + "\r\n")
		}
	}
	return out.Bytes()
}
