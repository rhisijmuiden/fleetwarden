//go:build windows

package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
)

const serviceName = "FleetWardenAgent"

func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type service struct{ log *slog.Logger }

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &agent.Agent{ConfigPath: agent.ConfigPath(), Log: s.log}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error("agent stopped", "err", err)
				if errors.Is(err, agent.ErrRemoved) {
					return false, 0
				}
				return true, 1
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func runService() {
	var log *slog.Logger
	if el, err := eventlog.Open(serviceName); err == nil {
		defer el.Close()
		log = slog.New(&eventLogWriter{el: el})
	} else {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	_ = svc.Run(serviceName, &service{log: log})
}
