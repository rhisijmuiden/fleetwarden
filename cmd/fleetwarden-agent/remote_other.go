//go:build !windows

package main

import "errors"

func remoteHelper([]string) error {
	return errors.New("the remote desktop helper only runs on Windows")
}
