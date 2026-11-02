//go:build windows

package install

import (
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/fsperm"
	"github.com/dotlane/dotlane/internal/ui"
	"golang.org/x/sys/windows"
)

// New returns the installer for this machine.
func New(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (Installer, error) {
	return NewWindows(cfg, configPath, daemon)
}

// NewWindows fills Windows with the real system's values.
func NewWindows(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (*Windows, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return nil, err
	}
	localAppData, err := os.UserCacheDir() // %LocalAppData%
	if err != nil {
		return nil, err
	}
	me, err := user.Current()
	if err != nil {
		return nil, err
	}
	if configPath, err = filepath.Abs(configPath); err != nil {
		return nil, err
	}
	return &Windows{
		Cfg: cfg, ConfigPath: configPath, Exe: exe,
		ServiceDir: filepath.Join(localAppData, "Programs", "dotlane"),
		LogDir:     filepath.Join(localAppData, "dotlane", "logs"),
		UserSID:    me.Uid, // on Windows, the account's SID
		UI:         ui.New(os.Stdout),
		Runner: ExecWinRunner{
			Dir:     cfg.StateDir,
			Admin:   windows.GetCurrentProcessToken().IsElevated(),
			Private: fsperm.PrivateDir,
		},
		Daemon:     daemon,
		Bindable:   bindable,
		Lookup:     net.DefaultResolver.LookupHost,
		PrivateDir: fsperm.PrivateDir,
		Owner:      fsperm.Owner,
		Firefox:    firefoxInstalled,
	}, nil
}

// firefoxInstalled looks for Firefox where its installer puts it.
func firefoxInstalled() bool {
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
		if dir := os.Getenv(env); dir != "" {
			if _, err := os.Stat(filepath.Join(dir, "Mozilla Firefox", "firefox.exe")); err == nil {
				return true
			}
		}
	}
	return false
}
