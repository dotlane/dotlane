//go:build !darwin && !windows

package daemon

// Linux limits Unix socket paths to 108 bytes, including the terminating NUL.
const (
	maxSocketPath = 107
	socketOS      = "this system"
)
