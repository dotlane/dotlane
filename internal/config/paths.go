package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDNSPort is where the DNS responder listens unless config.toml says otherwise. Windows
// sends a domain ending to a DNS server by address only (an NRPT rule has no port), so there it
// must be 53.
var DefaultDNSPort = func() int {
	if runtime.GOOS == "windows" {
		return 53
	}
	return 10053
}()

// DefaultPath is the config file used when neither --config nor $DOTLANE_CONFIG names one:
//
//	macOS    ~/.config/dotlane/config.toml
//	Linux    $XDG_CONFIG_HOME/dotlane/config.toml (~/.config when unset)
//	Windows  %AppData%\dotlane\config.toml
func DefaultPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "dotlane", "config.toml"), nil
	case "linux":
		if dir := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(dir) {
			return filepath.Join(dir, "dotlane", "config.toml"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "dotlane", "config.toml"), nil
}

// defaultStateDir holds the sites, the certificate authority and the control socket:
//
//	macOS    ~/Library/Application Support/dotlane
//	Linux    $XDG_STATE_HOME/dotlane (~/.local/state when unset), apart from the config
//	Windows  %LocalAppData%\dotlane, which does not roam to other machines
func defaultStateDir() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if dir := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(dir) {
			return filepath.Join(dir, "dotlane"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "state", "dotlane"), nil
	case "windows":
		dir, err := os.UserCacheDir() // %LocalAppData%
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "dotlane"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dotlane"), nil
}
