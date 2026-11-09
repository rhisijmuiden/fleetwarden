//go:build windows

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

// winConfig applies configurations on Windows: browser policies and
// registry values in the registry, scripts as SYSTEM or in the sessions of
// logged-on users.
type winConfig struct {
	log *slog.Logger
	// PolicyRoot replaces SOFTWARE\Policies (tests).
	PolicyRoot string
	// UserRoot replaces HKEY_USERS\<SID> with a key below HKCU (tests).
	UserRoot string
	// AsCurrentUser lists and runs as the user of this process instead of
	// the users of the sessions (tests, which do not run as SYSTEM).
	AsCurrentUser bool
	ScriptDir     string
}

func newPlatformConfigBackend(log *slog.Logger) ConfigBackend {
	return &winConfig{log: log, PolicyRoot: `SOFTWARE\Policies`, ScriptDir: filepath.Join(defaultStateDir(), "scripts")}
}

// NewWindowsTestConfigBackend writes policies below HKCU\<root> and runs
// scripts as the current user (tests).
func NewWindowsTestConfigBackend(root, scriptDir string) ConfigBackend {
	return &winConfig{PolicyRoot: root + `\Policies`, UserRoot: root + `\User`, AsCurrentUser: true, ScriptDir: scriptDir}
}

func (w *winConfig) policyHive() registry.Key {
	if w.AsCurrentUser {
		return registry.CURRENT_USER
	}
	return registry.LOCAL_MACHINE
}

func (w *winConfig) BrowserStore(b string) (PolicyStore, error) {
	var path string
	nested := false
	switch b {
	case agentproto.BrowserChrome:
		path = `Google\Chrome`
	case agentproto.BrowserEdge:
		path = `Microsoft\Edge`
	case agentproto.BrowserFirefox:
		path, nested = `Mozilla\Firefox`, true
	default:
		return nil, nil
	}
	return &regPolicyStore{root: w.policyHive(), path: w.PolicyRoot + `\` + path, nested: nested}, nil
}

func (w *winConfig) BrowserInstalled(b string) bool {
	exe := map[string]string{agentproto.BrowserChrome: "chrome.exe", agentproto.BrowserEdge: "msedge.exe", agentproto.BrowserFirefox: "firefox.exe"}[b]
	if exe == "" {
		return false
	}
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+exe, registry.QUERY_VALUE)
		if err == nil {
			k.Close()
			return true
		}
	}
	return false
}

func (w *winConfig) RegistryStore(hive, sid string) (PolicyStore, error) {
	switch hive {
	case agentproto.HiveMachine:
		if w.AsCurrentUser {
			return &regValueStore{root: registry.CURRENT_USER, prefix: w.PolicyRoot + `\Machine\`}, nil
		}
		return &regValueStore{root: registry.LOCAL_MACHINE}, nil
	case agentproto.HiveUser:
		if sid == "" {
			return nil, errors.New("no user")
		}
		if w.AsCurrentUser {
			return &regValueStore{root: registry.CURRENT_USER, prefix: w.UserRoot + `\`}, nil
		}
		// The hive of a logged-on user is loaded below HKEY_USERS.
		k, err := registry.OpenKey(registry.USERS, sid, registry.QUERY_VALUE)
		if err != nil {
			return nil, fmt.Errorf("the registry of %s is not loaded: %w", sid, err)
		}
		k.Close()
		return &regValueStore{root: registry.USERS, prefix: sid + `\`}, nil
	}
	return nil, nil
}

// Users lists the users of active sessions (console and remote desktop).
func (w *winConfig) Users() []ConfigUser {
	if w.AsCurrentUser {
		tok := windows.GetCurrentProcessToken()
		tu, err := tok.GetTokenUser()
		if err != nil {
			return nil
		}
		acc, dom, _, _ := tu.User.Sid.LookupAccount("")
		return []ConfigUser{{Session: 1, Name: dom + `\` + acc, SID: tu.User.Sid.String()}}
	}
	var info *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &n); err != nil {
		return nil
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	var out []ConfigUser
	for _, s := range unsafe.Slice(info, n) {
		if s.State != windows.WTSActive || s.SessionID == 0 {
			continue
		}
		var tok windows.Token
		if windows.WTSQueryUserToken(s.SessionID, &tok) != nil {
			continue
		}
		tu, err := tok.GetTokenUser()
		if err == nil {
			acc, dom, _, _ := tu.User.Sid.LookupAccount("")
			name := acc
			if dom != "" {
				name = dom + `\` + acc
			}
			out = append(out, ConfigUser{Session: s.SessionID, Name: name, SID: tu.User.Sid.String()})
		}
		tok.Close()
	}
	return out
}

// RunScript writes the script to a file and runs it without a window. A
// script for the user goes to the user's temporary folder and runs with the
// user's token in their session.
func (w *winConfig) RunScript(ctx context.Context, s agentproto.ScriptConfig, user *ConfigUser) agentproto.ScriptRun {
	run := agentproto.ScriptRun{At: time.Now().UTC()}
	if user != nil {
		run.User = user.Name
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	ext, body := ".ps1", "[Console]::OutputEncoding = [System.Text.Encoding]::UTF8\r\n"+s.Content
	if s.Shell == agentproto.ShellCmd {
		ext, body = ".cmd", "@chcp 65001 >nul\r\n"+s.Content
	}
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	dir := w.ScriptDir
	var token syscall.Token
	var env []string
	if user != nil && !w.AsCurrentUser {
		var tok windows.Token
		if err := windows.WTSQueryUserToken(user.Session, &tok); err != nil {
			run.Error = "the user's session is gone: " + err.Error()
			return run
		}
		defer tok.Close()
		token = syscall.Token(tok)
		e, err := userEnvironment(tok)
		if err != nil {
			run.Error = err.Error()
			return run
		}
		env = e
		dir = envValue(env, "TEMP")
		if dir == "" {
			run.Error = "the user's temporary folder is unknown"
			return run
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		run.Error = err.Error()
		return run
	}
	file := filepath.Join(dir, "fleetwarden-"+hex.EncodeToString(rnd[:])+ext)
	// PowerShell 5 reads a file without a byte order mark as ANSI.
	content := []byte(body)
	if ext == ".ps1" {
		content = append([]byte{0xEF, 0xBB, 0xBF}, content...)
	}
	if err := os.WriteFile(file, content, 0o600); err != nil {
		run.Error = err.Error()
		return run
	}
	defer os.Remove(file)

	sys := os.Getenv("SystemRoot")
	var cmd *exec.Cmd
	if ext == ".cmd" {
		cmd = exec.CommandContext(ctx, filepath.Join(sys, `System32\cmd.exe`))
		cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /c "` + file + `"`}
	} else {
		cmd = exec.CommandContext(ctx, filepath.Join(sys, `System32\WindowsPowerShell\v1.0\powershell.exe`),
			"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", file)
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags = windows.CREATE_NO_WINDOW
	if token != 0 {
		cmd.SysProcAttr.Token = token
		cmd.Env = env
		cmd.Dir = envValue(env, "USERPROFILE")
	} else {
		cmd.Dir = dir
	}
	cmd.WaitDelay = 10 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	err := cmd.Run()
	run.DurationMs = time.Since(start).Milliseconds()
	run.Output = strings.ReplaceAll(strings.ToValidUTF8(out.String(), ""), "\r\n", "\n")
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		run.Error, run.ExitCode = "the script did not finish in time", -1
	case errors.As(err, &ee):
		run.ExitCode = ee.ExitCode()
	case err != nil:
		run.Error, run.ExitCode = err.Error(), -1
	}
	return run
}

// userEnvironment returns the environment of a user's token.
func userEnvironment(tok windows.Token) ([]string, error) {
	var block *uint16
	if err := windows.CreateEnvironmentBlock(&block, tok, false); err != nil {
		return nil, err
	}
	defer windows.DestroyEnvironmentBlock(block) //nolint:errcheck
	var env []string
	for p := unsafe.Pointer(block); ; {
		s := windows.UTF16PtrToString((*uint16)(p))
		if s == "" {
			break
		}
		env = append(env, s)
		p = unsafe.Add(p, (len(windows.StringToUTF16(s)))*2)
	}
	return env, nil
}

func envValue(env []string, name string) string {
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

// ---- registry stores ----

// regPolicyStore keeps browser policies below a policy key.
type regPolicyStore struct {
	root   registry.Key
	path   string
	nested bool
}

func (s *regPolicyStore) read(name string) ([]regEntry, error) {
	k, err := registry.OpenKey(s.root, s.path, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer k.Close()
	var out []regEntry
	if e, ok := readEntry(k, "", name); ok {
		out = append(out, e)
	}
	var walk func(rel string) error
	walk = func(rel string) error {
		sk, err := registry.OpenKey(k, rel, registry.READ)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer sk.Close()
		names, _ := sk.ReadValueNames(-1)
		for _, n := range names {
			if e, ok := readEntry(sk, rel, n); ok {
				out = append(out, e)
			}
		}
		subs, _ := sk.ReadSubKeyNames(-1)
		for _, sub := range subs {
			if err := walk(rel + `\` + sub); err != nil {
				return err
			}
		}
		return nil
	}
	return out, walk(name)
}

func readEntry(k registry.Key, path, name string) (regEntry, bool) {
	_, typ, err := k.GetValue(name, nil)
	if err != nil {
		return regEntry{}, false
	}
	switch typ {
	case registry.DWORD, registry.QWORD:
		n, _, err := k.GetIntegerValue(name)
		return regEntry{Path: path, Name: name, Kind: "dword", N: n}, err == nil
	case registry.SZ, registry.EXPAND_SZ:
		v, _, err := k.GetStringValue(name)
		return regEntry{Path: path, Name: name, Kind: "sz", S: v}, err == nil
	}
	return regEntry{Path: path, Name: name, Kind: "other"}, true
}

func (s *regPolicyStore) Matches(name string, v any) (bool, bool, error) {
	cur, err := s.read(name)
	if err != nil {
		return false, false, err
	}
	return regEntries(cur) == regEntries(flattenPolicy(name, v, s.nested)), len(cur) > 0, nil
}

func (s *regPolicyStore) Set(name string, v any) error {
	if err := s.Delete(name); err != nil {
		return err
	}
	for _, e := range flattenPolicy(name, v, s.nested) {
		p := s.path
		if e.Path != "" {
			p += `\` + e.Path
		}
		k, _, err := registry.CreateKey(s.root, p, registry.SET_VALUE)
		if err != nil {
			return err
		}
		if e.Kind == "dword" {
			err = k.SetDWordValue(e.Name, uint32(e.N))
		} else {
			err = k.SetStringValue(e.Name, e.S)
		}
		k.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *regPolicyStore) Delete(name string) error {
	k, err := registry.OpenKey(s.root, s.path, registry.SET_VALUE|registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return deleteKeyTree(k, name)
}

func (s *regPolicyStore) Close() error { return nil }

func deleteKeyTree(parent registry.Key, name string) error {
	k, err := registry.OpenKey(parent, name, registry.ENUMERATE_SUB_KEYS|registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	subs, _ := k.ReadSubKeyNames(-1)
	for _, sub := range subs {
		if err := deleteKeyTree(k, sub); err != nil {
			k.Close()
			return err
		}
	}
	k.Close()
	return registry.DeleteKey(parent, name)
}

// regValueStore sets single registry values (the registry configuration).
type regValueStore struct {
	root   registry.Key
	prefix string
}

func (s *regValueStore) Matches(name string, v any) (bool, bool, error) {
	rv, ok := v.(RegValue)
	if !ok {
		return false, false, errors.New("not a registry value")
	}
	key, value := regValueOf(name)
	k, err := registry.OpenKey(s.root, s.prefix+key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return rv.Type == agentproto.RegDelete, false, nil
	}
	if err != nil {
		return false, false, err
	}
	defer k.Close()
	_, typ, err := k.GetValue(value, nil)
	if errors.Is(err, registry.ErrNotExist) {
		return rv.Type == agentproto.RegDelete, false, nil
	}
	if err != nil {
		return false, false, err
	}
	switch rv.Type {
	case agentproto.RegDelete:
		return false, true, nil
	case agentproto.RegString, agentproto.RegExpandString:
		want := uint32(registry.SZ)
		if rv.Type == agentproto.RegExpandString {
			want = registry.EXPAND_SZ
		}
		cur, _, err := k.GetStringValue(value)
		return err == nil && typ == want && cur == rv.Value, true, nil
	case agentproto.RegMultiString:
		cur, _, err := k.GetStringsValue(value)
		return err == nil && typ == registry.MULTI_SZ && strings.Join(cur, "\n") == strings.Join(multiLines(rv.Value), "\n"), true, nil
	case agentproto.RegDWord, agentproto.RegQWord:
		want := uint32(registry.DWORD)
		if rv.Type == agentproto.RegQWord {
			want = registry.QWORD
		}
		n, err := parseRegNumber(rv)
		if err != nil {
			return false, true, err
		}
		cur, _, err := k.GetIntegerValue(value)
		return err == nil && typ == want && cur == n, true, nil
	}
	return false, true, fmt.Errorf("unknown value type %q", rv.Type)
}

func multiLines(v string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(v, "\r\n", "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func parseRegNumber(rv RegValue) (uint64, error) {
	bits := 32
	if rv.Type == agentproto.RegQWord {
		bits = 64
	}
	return strconv.ParseUint(strings.TrimSpace(rv.Value), 0, bits)
}

func (s *regValueStore) Set(name string, v any) error {
	rv, ok := v.(RegValue)
	if !ok {
		return errors.New("not a registry value")
	}
	key, value := regValueOf(name)
	if rv.Type == agentproto.RegDelete {
		return s.Delete(name)
	}
	k, _, err := registry.CreateKey(s.root, s.prefix+key, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	switch rv.Type {
	case agentproto.RegString:
		return k.SetStringValue(value, rv.Value)
	case agentproto.RegExpandString:
		return k.SetExpandStringValue(value, rv.Value)
	case agentproto.RegMultiString:
		return k.SetStringsValue(value, multiLines(rv.Value))
	case agentproto.RegDWord, agentproto.RegQWord:
		n, err := parseRegNumber(rv)
		if err != nil {
			return err
		}
		if rv.Type == agentproto.RegDWord {
			return k.SetDWordValue(value, uint32(n))
		}
		return k.SetQWordValue(value, n)
	}
	return fmt.Errorf("unknown value type %q", rv.Type)
}

func (s *regValueStore) Delete(name string) error {
	key, value := regValueOf(name)
	k, err := registry.OpenKey(s.root, s.prefix+key, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(value); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

func (s *regValueStore) Close() error { return nil }
