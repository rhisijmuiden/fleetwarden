//go:build !linux && !windows

package terminal

// Start is not supported here.
func Start(Options) (Session, error) { return nil, ErrUnsupported }
