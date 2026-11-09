//go:build linux

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

func TestSystemdServices(t *testing.T) {
	var calls []string
	m := &linuxManage{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		switch args[0] {
		case "list-units":
			return []byte(`[{"unit":"ssh.service","load":"loaded","active":"active","sub":"running","description":"OpenBSD Secure Shell server"},
{"unit":"apt-daily.service","load":"loaded","active":"inactive","sub":"dead","description":"Daily apt download activities"},
{"unit":"networking.service","load":"loaded","active":"active","sub":"exited","description":"Raise network interfaces"},
{"unit":"bad.service","load":"loaded","active":"failed","sub":"failed","description":"Broken"},
{"unit":"gone.service","load":"not-found","active":"inactive","sub":"dead","description":"gone.service"}]`), nil
		case "list-unit-files":
			return []byte(`[{"unit_file":"ssh.service","state":"enabled","preset":"enabled"},{"unit_file":"apt-daily.service","state":"static","preset":null},
{"unit_file":"cups.service","state":"disabled","preset":"enabled"},{"unit_file":"bad.service","state":"masked","preset":null},
{"unit_file":"getty@.service","state":"enabled","preset":"enabled"}]`), nil
		}
		return nil, nil
	}}
	ss, err := m.Services(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]agentproto.Service{}
	for _, s := range ss {
		got[s.Name] = s
	}
	if len(ss) != 5 || got["ssh.service"].State != "running" || got["ssh.service"].StartType != "automatic" || !got["ssh.service"].CanStop ||
		got["apt-daily.service"].StartType != "static" || got["networking.service"].State != "exited" || got["bad.service"].State != "failed" ||
		got["bad.service"].StartType != "disabled" || got["cups.service"].State != "stopped" || got["cups.service"].StartType != "manual" {
		t.Errorf("services: %+v", ss)
	}
	calls = nil
	for _, a := range []string{agentproto.ServiceRestart, agentproto.ServiceAuto, agentproto.ServiceDisabled} {
		if err := m.ServiceAction(context.Background(), "ssh", a); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(calls, "|") != "restart ssh.service|unmask ssh.service|enable ssh.service|mask ssh.service" {
		t.Errorf("calls: %q", calls)
	}
}

func TestParseJournal(t *testing.T) {
	in := `{"__REALTIME_TIMESTAMP":"1762646400000000","PRIORITY":"3","SYSLOG_IDENTIFIER":"sshd","_PID":"812","MESSAGE":"error: kex_exchange_identification"}
{"__REALTIME_TIMESTAMP":"1762646300000000","PRIORITY":"6","_SYSTEMD_UNIT":"cron.service","MESSAGE":[104,105]}
{"__REALTIME_TIMESTAMP":"1762646200000000","PRIORITY":"4","_COMM":"kernel","MESSAGE":"Error in something"}
not json
`
	ev, more := parseJournal(strings.NewReader(in), agentproto.LogQuery{Max: 10})
	if len(ev) != 3 || more || ev[0].Provider != "sshd" || ev[0].Level != agentproto.LevelError || ev[0].PID != 812 ||
		ev[1].Message != "hi" || ev[1].Provider != "cron.service" || ev[2].Level != agentproto.LevelWarning || ev[0].Time.Unix() != 1762646400 {
		t.Errorf("events: %+v", ev)
	}
	ev, more = parseJournal(strings.NewReader(in), agentproto.LogQuery{Max: 1, Search: "ERROR"})
	if len(ev) != 1 || !more || ev[0].Provider != "sshd" {
		t.Errorf("search: %+v %v", ev, more)
	}
}
