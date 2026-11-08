//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// Network drives and printers on Windows. Changes that belong to a user
// (drive mappings, printer connections, the default printer) are made by a
// short script in the user's session, so Explorer and the user's programs
// see them at once; the agent checks them in the user's registry hive.

const storeScriptTimeout = 2 * time.Minute

// psq quotes a string for PowerShell.
func psq(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// run runs a short script for a store and turns a failure into an error.
func (w *winConfig) run(shell, script string, user *ConfigUser) error {
	ctx, cancel := context.WithTimeout(context.Background(), storeScriptTimeout)
	defer cancel()
	if shell == agentproto.ShellPowerShell {
		script = "$ErrorActionPreference = 'Stop'\n$ProgressPreference = 'SilentlyContinue'\n" + script
	}
	r := w.RunScript(ctx, agentproto.ScriptConfig{Shell: shell, Content: script}, user)
	if r.Error != "" {
		return errors.New(r.Error)
	}
	if r.ExitCode != 0 {
		out := strings.TrimSpace(r.Output)
		if out == "" {
			out = "exit code " + strconv.Itoa(r.ExitCode)
		}
		return errors.New(tailString(out, 400))
	}
	return nil
}

// userHive is where a user's registry hive is: HKEY_USERS\<SID>, or HKCU
// for the user of this process (tests).
func (w *winConfig) userHive(u ConfigUser) (registry.Key, string, error) {
	if w.AsCurrentUser {
		return registry.CURRENT_USER, "", nil
	}
	k, err := registry.OpenKey(registry.USERS, u.SID, registry.QUERY_VALUE)
	if err != nil {
		return 0, "", fmt.Errorf("the registry of %s is not loaded: %w", u.Name, err)
	}
	k.Close()
	return registry.USERS, u.SID + `\`, nil
}

func regPathString(root registry.Key, path, name string) (string, bool) {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, err == nil
}

func regKeyExists(root registry.Key, path string) bool {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

// ---- network drives ----

func (w *winConfig) DriveStore(u ConfigUser) (PolicyStore, error) {
	root, prefix, err := w.userHive(u)
	if err != nil {
		return nil, err
	}
	return &driveStore{w: w, user: u, root: root, prefix: prefix}, nil
}

type driveStore struct {
	w      *winConfig
	user   ConfigUser
	root   registry.Key
	prefix string
}

// mountKey is the Explorer key of a share: \\server\share -> ##server#share.
func mountKey(path string) string { return strings.ReplaceAll(path, `\`, "#") }

const mountPoints = `Software\Microsoft\Windows\CurrentVersion\Explorer\MountPoints2\`

func (s *driveStore) Matches(letter string, v any) (bool, bool, error) {
	d, ok := v.(DriveValue)
	if !ok {
		return false, false, errors.New("not a drive")
	}
	cur, present := regPathString(s.root, s.prefix+`Network\`+letter, "RemotePath")
	if !present || !strings.EqualFold(cur, d.Path) {
		return false, present, nil
	}
	if d.Label != "" {
		if l, _ := regPathString(s.root, s.prefix+mountPoints+mountKey(d.Path), "_LabelFromReg"); l != d.Label {
			return false, true, nil
		}
	}
	return true, true, nil
}

func (s *driveStore) Set(letter string, v any) error {
	d, ok := v.(DriveValue)
	if !ok {
		return errors.New("not a drive")
	}
	// Connect now. Without a password Windows may refuse (a PC without a
	// domain whose user has no account on the server); the mapping below
	// still makes Windows connect at the next logon and ask for the
	// password when the drive is opened.
	_ = s.w.run(agentproto.ShellCmd, fmt.Sprintf("net use %s: /delete /y >nul 2>&1\r\nnet use %s: \"%s\" /persistent:yes", letter, letter, d.Path), &s.user)
	key := s.prefix + `Network\` + letter
	if cur, _ := regPathString(s.root, key, "RemotePath"); !strings.EqualFold(cur, d.Path) {
		k, _, err := registry.CreateKey(s.root, key, registry.SET_VALUE)
		if err != nil {
			return err
		}
		err = errors.Join(k.SetStringValue("RemotePath", d.Path), k.SetStringValue("UserName", ""),
			k.SetStringValue("ProviderName", "Microsoft Windows Network"), k.SetDWordValue("ProviderType", 0x20000),
			k.SetDWordValue("ConnectionType", 1), k.SetDWordValue("DeferFlags", 4))
		k.Close()
		if err != nil {
			return err
		}
	}
	if d.Label != "" {
		k, _, err := registry.CreateKey(s.root, s.prefix+mountPoints+mountKey(d.Path), registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer k.Close()
		return k.SetStringValue("_LabelFromReg", d.Label)
	}
	return nil
}

func (s *driveStore) Delete(letter string) error {
	_ = s.w.run(agentproto.ShellCmd, fmt.Sprintf("net use %s: /delete /y", letter), &s.user)
	k, err := registry.OpenKey(s.root, s.prefix+"Network", registry.ENUMERATE_SUB_KEYS|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	return deleteKeyTree(k, letter)
}

func (s *driveStore) Close() error { return nil }

// ---- printers ----

const (
	printKey   = `SYSTEM\CurrentControlSet\Control\Print\`
	windowsKey = `Software\Microsoft\Windows NT\CurrentVersion\Windows`
)

// connKey is the registry key of a printer connection: \\server\name ->
// ,,server,name.
func connKey(path string) string { return strings.ReplaceAll(path, `\`, ",") }

// portName names the TCP/IP port of a printer after its address.
func portName(p agentproto.Printer) string {
	if p.Protocol == agentproto.PrinterLPR {
		return "FW_LPR_" + p.Address + "_" + p.Queue
	}
	port := p.Port
	if port == 0 {
		port = 9100
	}
	return fmt.Sprintf("FW_%s_%d", p.Address, port)
}

func (w *winConfig) PrinterStore(u *ConfigUser) (PolicyStore, error) {
	if u == nil {
		return &printerStore{w: w, root: registry.LOCAL_MACHINE}, nil
	}
	root, prefix, err := w.userHive(*u)
	if err != nil {
		return nil, err
	}
	return &printerStore{w: w, user: u, root: root, prefix: prefix}, nil
}

// printerStore holds the printers of the device (user nil) or of a user.
type printerStore struct {
	w      *winConfig
	user   *ConfigUser
	root   registry.Key
	prefix string
}

func (s *printerStore) Matches(name string, v any) (bool, bool, error) {
	if name == defaultPrinterName {
		want, _ := v.(string)
		cur, ok := regPathString(s.root, s.prefix+windowsKey, "Device")
		printer, _, _ := strings.Cut(cur, ",")
		return ok && strings.EqualFold(printer, want), ok, nil
	}
	p, ok := v.(agentproto.Printer)
	if !ok {
		return false, false, errors.New("not a printer")
	}
	if p.Kind == agentproto.PrinterShared {
		if s.user != nil {
			ok := regKeyExists(s.root, s.prefix+`Printers\Connections\`+connKey(p.Path))
			return ok, ok, nil
		}
		ok := regKeyExists(registry.LOCAL_MACHINE, printKey+`Connections\`+connKey(p.Path))
		return ok, ok, nil
	}
	key := printKey + `Printers\` + p.Name
	drv, ok := regPathString(registry.LOCAL_MACHINE, key, "Printer Driver")
	if !ok {
		return false, false, nil
	}
	port, _ := regPathString(registry.LOCAL_MACHINE, key, "Port")
	return strings.EqualFold(drv, p.Driver) && strings.EqualFold(port, portName(p)), true, nil
}

// ensureDriver installs a printer driver, from its .inf file when Windows
// does not have it yet.
func ensureDriver(p agentproto.Printer) string {
	if p.Driver == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "if (-not (Get-PrinterDriver -Name %s -ErrorAction SilentlyContinue)) {\n", psq(p.Driver))
	if p.DriverINF != "" {
		fmt.Fprintf(&b, "  $o = & pnputil.exe /add-driver %s /install 2>&1 | Out-String\n", psq(p.DriverINF))
		b.WriteString("  if ($LASTEXITCODE -notin 0, 259, 3010) { throw \"pnputil could not add the driver: $o\" }\n")
	}
	fmt.Fprintf(&b, "  Add-PrinterDriver -Name %s\n}\n", psq(p.Driver))
	return b.String()
}

func (s *printerStore) Set(name string, v any) error {
	if name == defaultPrinterName {
		want, _ := v.(string)
		if err := s.w.run(agentproto.ShellPowerShell, fmt.Sprintf(
			"Set-ItemProperty -Path 'HKCU:\\%s' -Name LegacyDefaultPrinterMode -Value 1 -Type DWord\n"+
				"(New-Object -ComObject WScript.Network).SetDefaultPrinter(%s)", windowsKey, psq(want)), s.user); err != nil {
			return fmt.Errorf("default printer %s: %w", want, err)
		}
		return nil
	}
	p, ok := v.(agentproto.Printer)
	if !ok {
		return errors.New("not a printer")
	}
	label := PrinterLabel(p)
	var err error
	switch {
	case p.Kind == agentproto.PrinterShared && s.user != nil:
		if p.Driver != "" {
			if err := s.w.run(agentproto.ShellPowerShell, ensureDriver(p), nil); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
		err = s.w.run(agentproto.ShellPowerShell, "Add-Printer -ConnectionName "+psq(p.Path), s.user)
	case p.Kind == agentproto.PrinterShared:
		if p.Driver != "" {
			if err := s.w.run(agentproto.ShellPowerShell, ensureDriver(p), nil); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
		// A connection for every user of the device; the spooler adds it to
		// a user's printers at logon.
		err = s.w.run(agentproto.ShellCmd, fmt.Sprintf(`rundll32.exe printui.dll,PrintUIEntry /ga /n"%s" /q`, p.Path), nil)
		if err == nil && !regKeyExists(registry.LOCAL_MACHINE, printKey+`Connections\`+connKey(p.Path)) {
			err = errors.New("Windows did not add the connection")
		}
	default:
		port := portName(p)
		var b strings.Builder
		b.WriteString(ensureDriver(p))
		fmt.Fprintf(&b, "if (-not (Get-PrinterPort -Name %s -ErrorAction SilentlyContinue)) {\n", psq(port))
		if p.Protocol == agentproto.PrinterLPR {
			fmt.Fprintf(&b, "  Add-PrinterPort -Name %s -LprHostAddress %s -LprQueueName %s -LprByteCounting\n", psq(port), psq(p.Address), psq(p.Queue))
		} else {
			fmt.Fprintf(&b, "  Add-PrinterPort -Name %s -PrinterHostAddress %s -PortNumber %d\n", psq(port), psq(p.Address), max(p.Port, 1))
		}
		b.WriteString("}\n")
		fmt.Fprintf(&b, "if (Get-Printer -Name %s -ErrorAction SilentlyContinue) {\n  Set-Printer -Name %s -DriverName %s -PortName %s\n} else {\n  Add-Printer -Name %s -DriverName %s -PortName %s\n}\n",
			psq(p.Name), psq(p.Name), psq(p.Driver), psq(port), psq(p.Name), psq(p.Driver), psq(port))
		err = s.w.run(agentproto.ShellPowerShell, b.String(), nil)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func (s *printerStore) Delete(name string) error {
	if name == defaultPrinterName {
		return nil // the user's default printer stays as it is
	}
	kind, id, _ := strings.Cut(name, ":")
	switch {
	case kind == agentproto.PrinterShared && s.user != nil:
		if !regKeyExists(s.root, s.prefix+`Printers\Connections\`+connKey(id)) {
			return nil
		}
		return s.w.run(agentproto.ShellPowerShell, "Remove-Printer -Name "+psq(id), s.user)
	case kind == agentproto.PrinterShared:
		if !regKeyExists(registry.LOCAL_MACHINE, printKey+`Connections\`+connKey(id)) {
			return nil
		}
		return s.w.run(agentproto.ShellCmd, fmt.Sprintf(`rundll32.exe printui.dll,PrintUIEntry /gd /n"%s" /q`, id), nil)
	}
	port, ok := regPathString(registry.LOCAL_MACHINE, printKey+`Printers\`+id, "Port")
	if !ok {
		return nil
	}
	script := "Remove-Printer -Name " + psq(id) + "\n"
	if strings.HasPrefix(port, "FW_") {
		// The port goes too when no other printer uses it.
		script += fmt.Sprintf("if (-not (Get-Printer | Where-Object PortName -eq %s)) { Remove-PrinterPort -Name %s -ErrorAction SilentlyContinue }\n", psq(port), psq(port))
	}
	return s.w.run(agentproto.ShellPowerShell, script, nil)
}

func (s *printerStore) Close() error { return nil }
