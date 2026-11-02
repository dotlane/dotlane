package daemon

// macOS limits Unix socket paths to 104 bytes, including the terminating NUL.
const (
	maxSocketPath = 103
	socketOS      = "macOS"
)
