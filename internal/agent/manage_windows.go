//go:build windows

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type winManage struct{}

func platformManage() ManageBackend { return winManage{} }

func isHidden(fi os.FileInfo) bool {
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&windows.FILE_ATTRIBUTE_HIDDEN != 0
	}
	return strings.HasPrefix(fi.Name(), ".")
}

// ---- processes ----

var (
	modPsapi                 = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = modPsapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// criticalProcesses stop Windows when they end.
var criticalProcesses = map[string]bool{
	"system": true, "smss.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true,
	"services.exe": true, "lsass.exe": true, "registry": true, "memory compression": true, "secure system": true,
}

type winSample struct {
	cpu uint64 // 100 ns units of kernel + user time
	p   agentproto.Process
}

func filetimeTicks(f windows.Filetime) uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

func commandLine(h windows.Handle) string {
	buf := make([]byte, 4096)
	for range 3 {
		var n uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), uint32(len(buf)), &n)
		if errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) && n > uint32(len(buf)) && n < 1<<20 {
			buf = make([]byte, n)
			continue
		}
		if err != nil {
			return ""
		}
		us := (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0]))
		s := us.String()
		if len(s) > 2000 {
			s = s[:2000]
		}
		return s
	}
	return ""
}

func processUser(h windows.Handle, cache map[string]string) string {
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) != nil {
		return ""
	}
	defer tok.Close()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return ""
	}
	sid := tu.User.Sid.String()
	if name, ok := cache[sid]; ok {
		return name
	}
	name := sid
	if acc, dom, _, err := tu.User.Sid.LookupAccount(""); err == nil {
		name = acc
		if dom != "" {
			name = dom + `\` + acc
		}
	}
	cache[sid] = name
	return name
}

func winSampleProcesses(users map[string]string, full bool) map[int]winSample {
	out := map[int]winSample{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		pid := int(e.ProcessID)
		p := agentproto.Process{PID: pid, PPID: int(e.ParentProcessID), Name: windows.UTF16ToString(e.ExeFile[:]), Threads: int(e.Threads)}
		if pid == 0 {
			p.Name = "System Idle Process"
		}
		p.Protected = pid <= 4 || criticalProcesses[strings.ToLower(p.Name)]
		var sess uint32
		if windows.ProcessIdToSessionId(e.ProcessID, &sess) == nil {
			p.Session = int(sess)
		}
		s := winSample{p: p}
		if pid != 0 {
			h, herr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
			if herr == nil {
				var c, x, k, u windows.Filetime
				if windows.GetProcessTimes(h, &c, &x, &k, &u) == nil {
					s.cpu = filetimeTicks(k) + filetimeTicks(u)
					if full && c.HighDateTime != 0 {
						t := time.Unix(0, c.Nanoseconds()).UTC().Truncate(time.Second)
						s.p.StartedAt = &t
					}
				}
				if full {
					var mc processMemoryCounters
					mc.cb = uint32(unsafe.Sizeof(mc))
					if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mc)), uintptr(mc.cb)); r != 0 {
						s.p.Memory = int64(mc.WorkingSetSize)
					}
					buf := make([]uint16, windows.MAX_LONG_PATH)
					n := uint32(len(buf))
					if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
						s.p.Path = windows.UTF16ToString(buf[:n])
					}
					s.p.User = processUser(h, users)
					s.p.Command = commandLine(h)
				}
				windows.CloseHandle(h)
			}
		}
		out[pid] = s
	}
	return out
}

var cpuSampleTime = 500 * time.Millisecond

func (winManage) Processes(ctx context.Context) ([]agentproto.Process, error) {
	users := map[string]string{}
	first := winSampleProcesses(users, false)
	t0 := time.Now()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(cpuSampleTime):
	}
	second := winSampleProcesses(users, true)
	// 100 ns units over all logical processors.
	elapsed := float64(time.Since(t0)/100) * float64(windows.GetActiveProcessorCount(windows.ALL_PROCESSOR_GROUPS))
	out := make([]agentproto.Process, 0, len(second))
	for pid, s := range second {
		p := s.p
		if f, ok := first[pid]; ok && s.cpu >= f.cpu && elapsed > 0 {
			p.CPU = float64(s.cpu-f.cpu) / elapsed * 100
		}
		out = append(out, p)
	}
	return out, nil
}

func (winManage) KillProcess(pid int) error {
	if pid <= 4 {
		return mErr(agentproto.ManageErrProtected, "system process")
	}
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return mErr(agentproto.ManageErrNotFound, "no process %d", pid)
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return mErr(agentproto.ManageErrDenied, "access denied")
		}
		return err
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		name := windows.UTF16ToString(buf[:n])
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		if criticalProcesses[strings.ToLower(name)] {
			return mErr(agentproto.ManageErrProtected, "%s is critical for Windows", name)
		}
	}
	if err := windows.TerminateProcess(h, 1); err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return mErr(agentproto.ManageErrDenied, "access denied")
		}
		return err
	}
	_, _ = windows.WaitForSingleObject(h, 5000)
	return nil
}

// ---- services ----

func openService(m *mgr.Mgr, name string, access uint32) (*mgr.Service, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.OpenService(m.Handle, p, access)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil, mErr(agentproto.ManageErrNotFound, "no service %s", name)
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, mErr(agentproto.ManageErrDenied, "access denied")
		}
		return nil, err
	}
	return &mgr.Service{Name: name, Handle: h}, nil
}

func svcState(s svc.State) string {
	switch s {
	case svc.Running:
		return "running"
	case svc.StartPending, svc.ContinuePending:
		return "starting"
	case svc.StopPending, svc.PausePending:
		return "stopping"
	case svc.Paused:
		return "paused"
	}
	return "stopped"
}

func svcStartType(c mgr.Config) string {
	switch c.StartType {
	case mgr.StartAutomatic, windows.SERVICE_BOOT_START, windows.SERVICE_SYSTEM_START:
		if c.DelayedAutoStart {
			return "automatic-delayed"
		}
		return agentproto.ServiceAuto
	case mgr.StartDisabled:
		return agentproto.ServiceDisabled
	}
	return agentproto.ServiceManual
}

func (winManage) Services(ctx context.Context) ([]agentproto.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer m.Disconnect()
	names, err := m.ListServices()
	if err != nil {
		return nil, err
	}
	out := make([]agentproto.Service, 0, len(names))
	for _, n := range names {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s, err := openService(m, n, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
		if err != nil {
			continue
		}
		e := agentproto.Service{Name: n, DisplayName: n}
		if st, err := s.Query(); err == nil {
			e.State, e.PID, e.CanStop = svcState(st.State), int(st.ProcessId), st.Accepts&svc.AcceptStop != 0
		}
		if c, err := s.Config(); err == nil {
			// Drivers are listed by ListServices too on some versions.
			if c.ServiceType&(windows.SERVICE_KERNEL_DRIVER|windows.SERVICE_FILE_SYSTEM_DRIVER) != 0 {
				s.Close()
				continue
			}
			if c.DisplayName != "" {
				e.DisplayName = c.DisplayName
			}
			e.Description, e.StartType, e.Account, e.Path = c.Description, svcStartType(c), c.ServiceStartName, c.BinaryPathName
		}
		s.Close()
		out = append(out, e)
	}
	return out, nil
}

// waitState waits until a service reaches a state.
func waitState(ctx context.Context, s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return mErr(agentproto.ManageErrFailed, "the service did not reach the state %s in time (now %s)", svcState(want), svcState(st.State))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func stopWinService(ctx context.Context, s *mgr.Service) error {
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Stopped {
		return nil
	}
	if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		if errors.Is(err, windows.ERROR_DEPENDENT_SERVICES_RUNNING) {
			return mErr(agentproto.ManageErrFailed, "other running services depend on it; stop those first")
		}
		if errors.Is(err, windows.ERROR_INVALID_SERVICE_CONTROL) {
			return mErr(agentproto.ManageErrFailed, "the service cannot be stopped now")
		}
		return err
	}
	return waitState(ctx, s, svc.Stopped, 60*time.Second)
}

func startWinService(ctx context.Context, s *mgr.Service) error {
	if err := s.Start(); err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return nil
		}
		if errors.Is(err, windows.ERROR_SERVICE_DISABLED) {
			return mErr(agentproto.ManageErrFailed, "the service is disabled; change the startup type first")
		}
		return err
	}
	return waitState(ctx, s, svc.Running, 60*time.Second)
}

func (winManage) ServiceAction(ctx context.Context, name, action string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := openService(m, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG|windows.SERVICE_CHANGE_CONFIG|
		windows.SERVICE_START|windows.SERVICE_STOP|windows.SERVICE_ENUMERATE_DEPENDENTS)
	if err != nil {
		return err
	}
	defer s.Close()
	switch action {
	case agentproto.ServiceStart:
		return startWinService(ctx, s)
	case agentproto.ServiceStop:
		return stopWinService(ctx, s)
	case agentproto.ServiceRestart:
		if err := stopWinService(ctx, s); err != nil {
			return err
		}
		return startWinService(ctx, s)
	}
	c, err := s.Config()
	if err != nil {
		return err
	}
	c.DelayedAutoStart = false
	switch action {
	case agentproto.ServiceAuto:
		c.StartType = mgr.StartAutomatic
	case agentproto.ServiceManual:
		c.StartType = mgr.StartManual
	case agentproto.ServiceDisabled:
		c.StartType = mgr.StartDisabled
	}
	return s.UpdateConfig(c)
}

// ---- event logs (wevtapi) ----

var (
	modWevtapi                   = windows.NewLazySystemDLL("wevtapi.dll")
	procEvtQuery                 = modWevtapi.NewProc("EvtQuery")
	procEvtNext                  = modWevtapi.NewProc("EvtNext")
	procEvtRender                = modWevtapi.NewProc("EvtRender")
	procEvtClose                 = modWevtapi.NewProc("EvtClose")
	procEvtOpenLog               = modWevtapi.NewProc("EvtOpenLog")
	procEvtGetLogInfo            = modWevtapi.NewProc("EvtGetLogInfo")
	procEvtOpenPublisherMetadata = modWevtapi.NewProc("EvtOpenPublisherMetadata")
	procEvtFormatMessage         = modWevtapi.NewProc("EvtFormatMessage")
)

const (
	evtQueryChannelPath      = 0x1
	evtQueryReverseDirection = 0x200
	evtRenderEventXML        = 1
	evtOpenChannelPath       = 1
	evtLogNumberOfLogRecords = 5
	evtFormatMessageEvent    = 1
	evtVarTypeUInt64         = 10
)

func evtClose(h uintptr) {
	if h != 0 {
		procEvtClose.Call(h) //nolint:errcheck
	}
}

// winLogs are offered when they exist on the machine.
var winLogs = []string{
	"Application", "System", "Security", "Setup",
	"Microsoft-Windows-PowerShell/Operational",
	"Microsoft-Windows-Windows Defender/Operational",
	"Microsoft-Windows-TaskScheduler/Operational",
	"Microsoft-Windows-WindowsUpdateClient/Operational",
	"Microsoft-Windows-TerminalServices-LocalSessionManager/Operational",
	"Microsoft-Windows-Bits-Client/Operational",
}

func (winManage) LogSources(ctx context.Context) ([]agentproto.LogSource, error) {
	if err := procEvtQuery.Find(); err != nil {
		return nil, mErr(agentproto.ManageErrUnsupported, "the event log API is not available")
	}
	out := []agentproto.LogSource{}
	for _, n := range winLogs {
		p, _ := windows.UTF16PtrFromString(n)
		h, _, _ := procEvtOpenLog.Call(0, uintptr(unsafe.Pointer(p)), evtOpenChannelPath)
		if h == 0 {
			continue
		}
		src := agentproto.LogSource{Name: n}
		// EVT_VARIANT: 8 bytes value, 4 count, 4 type.
		var v [16]byte
		var used uint32
		if r, _, _ := procEvtGetLogInfo.Call(h, evtLogNumberOfLogRecords, 16, uintptr(unsafe.Pointer(&v[0])), uintptr(unsafe.Pointer(&used))); r != 0 {
			if *(*uint32)(unsafe.Pointer(&v[12])) == evtVarTypeUInt64 {
				src.Records = int64(*(*uint64)(unsafe.Pointer(&v[0])))
			}
		}
		evtClose(h)
		out = append(out, src)
	}
	return out, nil
}

func (winManage) QueryLog(ctx context.Context, q agentproto.LogQuery) ([]agentproto.LogEvent, bool, error) {
	if !validLogName(q.Source) {
		return nil, false, mErr(agentproto.ManageErrInvalid, "invalid log name")
	}
	if q.Provider != "" && !validLogName(q.Provider) {
		return nil, false, mErr(agentproto.ManageErrInvalid, "invalid source name")
	}
	if err := procEvtQuery.Find(); err != nil {
		return nil, false, mErr(agentproto.ManageErrUnsupported, "the event log API is not available")
	}
	path, _ := windows.UTF16PtrFromString(q.Source)
	query, _ := windows.UTF16PtrFromString(eventXPath(q, time.Now()))
	res, _, err := procEvtQuery.Call(0, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(query)), evtQueryChannelPath|evtQueryReverseDirection)
	if res == 0 {
		if errors.Is(err, windows.ERROR_EVT_CHANNEL_NOT_FOUND) {
			return nil, false, mErr(agentproto.ManageErrNotFound, "no log %q", q.Source)
		}
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, false, mErr(agentproto.ManageErrDenied, "access denied")
		}
		return nil, false, fmt.Errorf("EvtQuery: %w", err)
	}
	defer evtClose(res)
	publishers := map[string]uintptr{}
	defer func() {
		for _, h := range publishers {
			evtClose(h)
		}
	}()
	needle := strings.ToLower(q.Search)
	out := []agentproto.LogEvent{}
	scanned := 0
	handles := make([]uintptr, 64)
	for {
		if ctx.Err() != nil {
			return out, true, nil
		}
		var got uint32
		r, _, err := procEvtNext.Call(res, uintptr(len(handles)), uintptr(unsafe.Pointer(&handles[0])), 5000, 0, uintptr(unsafe.Pointer(&got)))
		if r == 0 {
			if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
				return out, false, nil
			}
			return out, false, fmt.Errorf("EvtNext: %w", err)
		}
		for i := range int(got) {
			h := handles[i]
			if len(out) <= q.Max {
				e, ok := renderEvent(h, publishers)
				scanned++
				if ok && (needle == "" || strings.Contains(strings.ToLower(e.Message), needle) || strings.Contains(strings.ToLower(e.Provider), needle)) {
					out = append(out, e)
				}
			}
			evtClose(h)
		}
		if len(out) > q.Max {
			return out[:q.Max], true, nil
		}
		if scanned >= 100000 { // a search over a huge log
			return out, true, nil
		}
	}
}

func renderEvent(h uintptr, publishers map[string]uintptr) (agentproto.LogEvent, bool) {
	var used, props uint32
	procEvtRender.Call(0, h, evtRenderEventXML, 0, 0, uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props))) //nolint:errcheck
	if used == 0 || used > 4<<20 {
		return agentproto.LogEvent{}, false
	}
	buf := make([]uint16, used/2+1)
	if r, _, _ := procEvtRender.Call(0, h, evtRenderEventXML, uintptr(len(buf)*2), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used)), uintptr(unsafe.Pointer(&props))); r == 0 {
		return agentproto.LogEvent{}, false
	}
	e, err := parseEventXML(windows.UTF16ToString(buf))
	if err != nil {
		return agentproto.LogEvent{}, false
	}
	if e.Provider != "" {
		pub, ok := publishers[e.Provider]
		if !ok {
			p, _ := windows.UTF16PtrFromString(e.Provider)
			pub, _, _ = procEvtOpenPublisherMetadata.Call(0, uintptr(unsafe.Pointer(p)), 0, 0, 0)
			publishers[e.Provider] = pub
		}
		if pub != 0 {
			if msg := formatEventMessage(pub, h); msg != "" {
				e.Message = msg
			}
		}
	}
	if len(e.Message) > 8000 {
		e.Message = e.Message[:8000]
	}
	return e, true
}

func formatEventMessage(pub, h uintptr) string {
	var used uint32
	procEvtFormatMessage.Call(pub, h, 0, 0, 0, evtFormatMessageEvent, 0, 0, uintptr(unsafe.Pointer(&used))) //nolint:errcheck
	if used == 0 || used > 1<<20 {
		return ""
	}
	buf := make([]uint16, used)
	if r, _, _ := procEvtFormatMessage.Call(pub, h, 0, 0, 0, evtFormatMessageEvent, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used))); r == 0 {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf))
}

// ---- drives ----

func (winManage) Roots() ([]agentproto.FileEntry, error) {
	buf := make([]uint16, 512)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil {
		return nil, err
	}
	var out []agentproto.FileEntry
	// The buffer holds NUL-separated strings, so split it by hand.
	start := 0
	for i := 0; i < int(n); i++ {
		if buf[i] != 0 {
			continue
		}
		if i > start {
			root := windows.UTF16ToString(buf[start:i])
			p, _ := windows.UTF16PtrFromString(root)
			e := agentproto.FileEntry{Name: strings.TrimSuffix(root, `\`), Path: root, Dir: true}
			switch windows.GetDriveType(p) {
			case windows.DRIVE_REMOVABLE:
				e.Drive = "removable"
			case windows.DRIVE_REMOTE:
				e.Drive = "network"
			case windows.DRIVE_CDROM:
				e.Drive = "cdrom"
			case windows.DRIVE_RAMDISK, windows.DRIVE_FIXED:
				e.Drive = "fixed"
			default:
				e.Drive = "other"
			}
			if e.Drive != "cdrom" && e.Drive != "network" {
				var free, total, totalFree uint64
				if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil {
					e.Size, e.Free = int64(total), int64(free)
				}
			}
			out = append(out, e)
		}
		start = i + 1
	}
	return out, nil
}
