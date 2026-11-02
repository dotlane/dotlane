//go:build !windows

package fsperm

import "os"

func restrictDir(dir string) error { return os.Chmod(dir, 0o700) }

func restrictFile(path string) error { return os.Chmod(path, 0o600) }
