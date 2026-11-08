//go:build !unix

package server

func freeBytes(string) (uint64, bool) { return 0, false }
