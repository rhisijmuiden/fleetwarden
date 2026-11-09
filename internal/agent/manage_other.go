//go:build !linux && !windows

package agent

import (
	"context"

	"github.com/rhisijmuiden/fleetwarden/internal/agentproto"
)

type otherManage struct{}

func platformManage() ManageBackend { return otherManage{} }

var errManageUnsupported = mErr(agentproto.ManageErrUnsupported, "not supported on this platform")

func (otherManage) Processes(context.Context) ([]agentproto.Process, error) {
	return nil, errManageUnsupported
}
func (otherManage) KillProcess(int) error { return errManageUnsupported }
func (otherManage) Services(context.Context) ([]agentproto.Service, error) {
	return nil, errManageUnsupported
}
func (otherManage) ServiceAction(context.Context, string, string) error { return errManageUnsupported }
func (otherManage) LogSources(context.Context) ([]agentproto.LogSource, error) {
	return nil, errManageUnsupported
}
func (otherManage) QueryLog(context.Context, agentproto.LogQuery) ([]agentproto.LogEvent, bool, error) {
	return nil, false, errManageUnsupported
}
func (otherManage) Roots() ([]agentproto.FileEntry, error) {
	return []agentproto.FileEntry{{Name: "/", Path: "/", Dir: true, Drive: "fixed"}}, nil
}
