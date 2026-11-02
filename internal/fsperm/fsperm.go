// Package fsperm keeps Dotlane's state private to the user who runs it: the certificate
// authority's keys, the site registry and the control socket.
//
// On macOS and Linux that is file modes (0700 folders, 0600 files). Windows ignores modes, so
// there the state folder gets an access control list that admits only its owner and SYSTEM, and
// everything created inside inherits it.
package fsperm

import "os"

// PrivateDir creates dir, if needed, and makes it accessible to the current user only.
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return restrictDir(dir)
}

// PrivateFile makes an existing file readable and writable by the current user only. On Windows
// it does nothing: the file inherits its folder's access control list (see PrivateDir).
func PrivateFile(path string) error { return restrictFile(path) }
