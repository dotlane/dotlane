//go:build !unix

package main

// writable reports true: Windows has no sudo for updateCommand to suggest.
func writable(string) bool { return true }
