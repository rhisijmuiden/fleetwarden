//go:build linux

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type linuxManage struct {
	proc string // /proc, replaced in tests
	// run runs a command and returns its standard output.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

func platformManage() ManageBackend { return &linuxManage{proc: "/proc", run: runOutput} }

func runOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, mErr(agentproto.ManageErrUnsupported, "%s is not installed", name)
		}
		return out, mErr(agentproto.ManageErrFailed, "%s", firstLine(msg))
	}
	return out, nil
}

// clockTicks is USER_HZ, 100 on every Linux architecture we build for.
const clockTicks = 100

type procSample struct {
	ticks uint64
	p     agentproto.Process
}

func (m *linuxManage) sample(users map[string]string, boot time.Time) map[int]procSample {
	out := map[int]procSample{}
	ents, err := os.ReadDir(m.proc)
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(m.proc, e.Name())
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ... ; comm may contain spaces and parentheses.
		open, close := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
		if open < 0 || close < open {
			continue
		}
		f := strings.Fields(string(stat[close+1:]))
		if len(f) < 22 {
			continue
		}
		p := agentproto.Process{PID: pid, Name: string(stat[open+1 : close])}
		p.PPID, _ = strconv.Atoi(f[1])
		utime, _ := strconv.ParseUint(f[11], 10, 64)
		stime, _ := strconv.ParseUint(f[12], 10, 64)
		p.Threads, _ = strconv.Atoi(f[17])
		if start, err := strconv.ParseUint(f[19], 10, 64); err == nil && !boot.IsZero() {
			t := boot.Add(time.Duration(start) * time.Second / clockTicks).UTC().Truncate(time.Second)
			p.StartedAt = &t
		}
		if rss, err := strconv.ParseInt(f[21], 10, 64); err == nil {
			p.Memory = rss * int64(os.Getpagesize())
		}
		if st, err := os.Stat(filepath.Join(dir, "stat")); err == nil {
			if s, ok := st.Sys().(*syscall.Stat_t); ok {
				uid := strconv.FormatUint(uint64(s.Uid), 10)
				name, ok := users[uid]
				if !ok {
					name = uid
					if u, err := user.LookupId(uid); err == nil {
						name = u.Username
					}
					users[uid] = name
				}
				p.User = name
			}
		}
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			p.Path = strings.TrimSuffix(exe, " (deleted)")
		}
		if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil && len(cmd) > 0 {
			c := strings.TrimSpace(strings.ReplaceAll(string(bytes.TrimRight(cmd, "\x00")), "\x00", " "))
			if len(c) > 2000 {
				c = c[:2000]
			}
			p.Command = c
		}
		// Kernel threads: children of kthreadd (pid 2), and pid 1.
		p.Protected = pid == 1 || pid == 2 || p.PPID == 2
		out[pid] = procSample{ticks: utime + stime, p: p}
	}
	return out
}

func (m *linuxManage) bootTime() time.Time {
	raw, err := os.ReadFile(filepath.Join(m.proc, "stat"))
	if err != nil {
		return time.Time{}
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

func cpuCount() int {
	n := 0
	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		for _, l := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(l, "cpu") && len(l) > 3 && l[3] >= '0' && l[3] <= '9' {
				n++
			}
		}
	}
	return max(n, 1)
}

// cpuSampleTime is the window CPU use is measured over.
var cpuSampleTime = 500 * time.Millisecond

func (m *linuxManage) Processes(ctx context.Context) ([]agentproto.Process, error) {
	users := map[string]string{}
	boot := m.bootTime()
	first := m.sample(users, boot)
	t0 := time.Now()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(cpuSampleTime):
	}
	second := m.sample(users, boot)
	elapsed := time.Since(t0).Seconds() * clockTicks * float64(cpuCount())
	out := make([]agentproto.Process, 0, len(second))
	for pid, s := range second {
		p := s.p
		if f, ok := first[pid]; ok && s.ticks >= f.ticks && elapsed > 0 {
			p.CPU = float64(s.ticks-f.ticks) / elapsed * 100
		}
		out = append(out, p)
	}
	return out, nil
}

func (m *linuxManage) KillProcess(pid int) error {
	if pid == 1 {
		return mErr(agentproto.ManageErrProtected, "init cannot be stopped")
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return mErr(agentproto.ManageErrNotFound, "no process %d", pid)
		}
		return err
	}
	for range 50 { // give it 5 seconds, then force
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(pid, 0) != nil || m.zombie(pid) {
			return nil
		}
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// zombie reports whether a process has ended and waits for its parent.
func (m *linuxManage) zombie(pid int) bool {
	raw, err := os.ReadFile(filepath.Join(m.proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return true
	}
	i := bytes.LastIndexByte(raw, ')')
	return i >= 0 && i+2 < len(raw) && raw[i+2] == 'Z'
}

// ---- systemd ----

type systemdUnit struct {
	Unit        string `json:"unit"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	Description string `json:"description"`
}

type systemdUnitFile struct {
	UnitFile string `json:"unit_file"`
	State    string `json:"state"`
}

func (m *linuxManage) Services(ctx context.Context) ([]agentproto.Service, error) {
	raw, err := m.run(ctx, "systemctl", "list-units", "--type=service", "--all", "--output=json", "--no-pager", "--plain")
	if err != nil {
		return nil, err
	}
	var units []systemdUnit
	if err := json.Unmarshal(raw, &units); err != nil {
		return nil, fmt.Errorf("systemctl list-units: %w", err)
	}
	files := map[string]string{}
	if raw, err := m.run(ctx, "systemctl", "list-unit-files", "--type=service", "--output=json", "--no-pager"); err == nil {
		var uf []systemdUnitFile
		if json.Unmarshal(raw, &uf) == nil {
			for _, f := range uf {
				files[filepath.Base(f.UnitFile)] = f.State
			}
		}
	}
	seen := map[string]bool{}
	out := make([]agentproto.Service, 0, len(units))
	for _, u := range units {
		if u.Load == "not-found" || strings.HasSuffix(strings.TrimSuffix(u.Unit, ".service"), "@") {
			continue
		}
		seen[u.Unit] = true
		out = append(out, agentproto.Service{
			Name: u.Unit, DisplayName: strings.TrimSuffix(u.Unit, ".service"), Description: u.Description,
			State: systemdState(u.Active, u.Sub), StartType: systemdStartType(files[u.Unit]), CanStop: u.Active == "active",
		})
	}
	// Installed but never loaded (disabled) units.
	for name, state := range files {
		if seen[name] || strings.Contains(name, "@") {
			continue
		}
		out = append(out, agentproto.Service{Name: name, DisplayName: strings.TrimSuffix(name, ".service"), State: "stopped",
			StartType: systemdStartType(state)})
	}
	return out, nil
}

func systemdState(active, sub string) string {
	switch active {
	case "active", "reloading":
		if sub == "exited" {
			return "exited"
		}
		return "running"
	case "activating":
		return "starting"
	case "deactivating":
		return "stopping"
	case "failed":
		return "failed"
	}
	return "stopped"
}

func systemdStartType(state string) string {
	switch state {
	case "enabled", "enabled-runtime", "alias":
		return agentproto.ServiceAuto
	case "disabled":
		return agentproto.ServiceManual
	case "masked", "masked-runtime":
		return agentproto.ServiceDisabled
	case "static", "indirect", "generated", "transient":
		return "static"
	}
	return agentproto.ServiceManual
}

func (m *linuxManage) ServiceAction(ctx context.Context, name, action string) error {
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	// start, stop and restart wait for the job, so the answer tells
	// whether it worked.
	switch action {
	case agentproto.ServiceStart, agentproto.ServiceStop, agentproto.ServiceRestart:
		_, err := m.run(ctx, "systemctl", action, name)
		return err
	case agentproto.ServiceAuto, agentproto.ServiceManual:
		if _, err := m.run(ctx, "systemctl", "unmask", name); err != nil {
			return err
		}
		verb := "enable"
		if action == agentproto.ServiceManual {
			verb = "disable"
		}
		_, err := m.run(ctx, "systemctl", verb, name)
		return err
	case agentproto.ServiceDisabled:
		_, err := m.run(ctx, "systemctl", "mask", name)
		return err
	}
	return mErr(agentproto.ManageErrInvalid, "invalid action")
}

// ---- journal ----

func (m *linuxManage) LogSources(ctx context.Context) ([]agentproto.LogSource, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, mErr(agentproto.ManageErrUnsupported, "journalctl is not installed")
	}
	return []agentproto.LogSource{{Name: "journal"}, {Name: "kernel"}}, nil
}

// journalLevel maps a syslog priority to the Windows levels.
func journalLevel(prio string) int {
	switch prio {
	case "0", "1", "2":
		return agentproto.LevelCritical
	case "3":
		return agentproto.LevelError
	case "4":
		return agentproto.LevelWarning
	case "5", "6":
		return agentproto.LevelInfo
	}
	return agentproto.LevelVerbose
}

// journalPriority is the highest priority number to show for a level.
func journalPriority(level int) string {
	switch level {
	case agentproto.LevelCritical:
		return "2"
	case agentproto.LevelError:
		return "3"
	case agentproto.LevelWarning:
		return "4"
	case agentproto.LevelInfo:
		return "6"
	}
	return "7"
}

func (m *linuxManage) QueryLog(ctx context.Context, q agentproto.LogQuery) ([]agentproto.LogEvent, bool, error) {
	args := []string{"--output=json", "--no-pager", "--reverse", "--quiet", "--all", "--output-fields=MESSAGE,PRIORITY,SYSLOG_IDENTIFIER,_SYSTEMD_UNIT,_COMM,_PID,_UID,__REALTIME_TIMESTAMP"}
	switch q.Source {
	case "", "journal":
	case "kernel":
		args = append(args, "--dmesg")
	default:
		return nil, false, mErr(agentproto.ManageErrNotFound, "unknown log %q", q.Source)
	}
	if q.MaxLevel > 0 {
		args = append(args, "--priority="+journalPriority(q.MaxLevel))
	}
	if !q.Since.IsZero() {
		args = append(args, "--since=@"+strconv.FormatInt(q.Since.Unix(), 10))
	}
	if q.Provider != "" {
		if !validServiceName(q.Provider) {
			return nil, false, mErr(agentproto.ManageErrInvalid, "invalid unit")
		}
		if strings.Contains(q.Provider, ".") {
			args = append(args, "--unit="+q.Provider)
		} else {
			args = append(args, "--identifier="+q.Provider)
		}
	}
	// Without a search, journalctl stops at the limit itself; with one we
	// read further and filter here.
	limit := q.Max + 1
	if q.Search != "" {
		limit = max(limit, 50000)
	}
	args = append(args, "--lines="+strconv.Itoa(limit))
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "SYSTEMD_PAGER=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, false, mErr(agentproto.ManageErrUnsupported, "journalctl is not installed")
		}
		return nil, false, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	ev, more := parseJournal(stdout, q)
	return ev, more, nil
}

// parseJournal reads journalctl JSON lines.
func parseJournal(r interface{ Read([]byte) (int, error) }, q agentproto.LogQuery) ([]agentproto.LogEvent, bool) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	needle := strings.ToLower(q.Search)
	out := []agentproto.LogEvent{}
	for sc.Scan() {
		var m map[string]json.RawMessage
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		str := func(k string) string {
			raw, ok := m[k]
			if !ok {
				return ""
			}
			var s string
			if json.Unmarshal(raw, &s) == nil {
				return s
			}
			var b []byte // binary fields come as arrays of bytes
			if json.Unmarshal(raw, &b) == nil {
				return string(b)
			}
			return ""
		}
		e := agentproto.LogEvent{Message: str("MESSAGE"), Level: journalLevel(str("PRIORITY"))}
		e.Provider = str("SYSLOG_IDENTIFIER")
		if u := str("_SYSTEMD_UNIT"); u != "" && e.Provider == "" {
			e.Provider = u
		}
		if e.Provider == "" {
			e.Provider = str("_COMM")
		}
		if e.Provider == "" && q.Source == "kernel" {
			e.Provider = "kernel"
		}
		e.PID, _ = strconv.Atoi(str("_PID"))
		if us, err := strconv.ParseInt(str("__REALTIME_TIMESTAMP"), 10, 64); err == nil {
			e.Time = time.UnixMicro(us).UTC()
		}
		if needle != "" && !strings.Contains(strings.ToLower(e.Message), needle) && !strings.Contains(strings.ToLower(e.Provider), needle) {
			continue
		}
		if len(out) >= q.Max {
			return out, true
		}
		if len(e.Message) > 8000 {
			e.Message = e.Message[:8000]
		}
		out = append(out, e)
	}
	return out, false
}

func (m *linuxManage) Roots() ([]agentproto.FileEntry, error) {
	e := agentproto.FileEntry{Name: "/", Path: "/", Dir: true, Drive: "fixed"}
	var st syscall.Statfs_t
	if syscall.Statfs("/", &st) == nil {
		e.Size = int64(st.Blocks) * st.Bsize
		e.Free = int64(st.Bavail) * st.Bsize
	}
	return []agentproto.FileEntry{e}, nil
}
