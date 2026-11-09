//go:build windows

package main

import (
	"errors"
	"flag"

	"github.com/rhisijmuiden/fleetwarden/internal/agent"
)

func remoteHelper(args []string) error {
	fs := flag.NewFlagSet("remote-helper", flag.ContinueOnError)
	pipe := fs.String("pipe", "", "the agent's pipe")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *pipe == "" {
		return errors.New("--pipe is required")
	}
	return agent.RunRemoteHelper(*pipe)
}
