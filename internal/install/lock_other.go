//go:build !unix && !windows

package install

import "os"

// tryLock does nothing where there is no login service to protect.
func tryLock(*os.File) (bool, error) { return true, nil }
