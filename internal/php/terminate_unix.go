//go:build !windows

package php

import (
	"os"
	"syscall"
)

// terminate asks a runtime to stop; PHP-FPM and FrankenPHP both stop their workers on SIGTERM.
func terminate(p *os.Process) { p.Signal(syscall.SIGTERM) }
