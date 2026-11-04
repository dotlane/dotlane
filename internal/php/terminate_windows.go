package php

import "os"

// terminate stops a runtime: Windows has no SIGTERM to send.
func terminate(p *os.Process) { p.Kill() }
