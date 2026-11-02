package daemon

// Windows' AF_UNIX takes paths of up to 108 bytes, including the terminating NUL.
const (
	maxSocketPath = 107
	socketOS      = "Windows"
)
