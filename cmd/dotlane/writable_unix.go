//go:build unix

package main

import "golang.org/x/sys/unix"

// writable reports whether this user can write in dir.
func writable(dir string) bool { return unix.Access(dir, unix.W_OK) == nil }
