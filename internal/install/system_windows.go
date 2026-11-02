//go:build windows

package install

import (
	"context"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"unsafe"

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
			Dir:           cfg.StateDir,
			SID:           me.Uid,
			Admin:         windows.GetCurrentProcessToken().IsElevated(),
			NoAdminRights: noAdminRights(),
			Private:       fsperm.PrivateDir,
		},
		Daemon:     daemon,
		Bindable:   bindable,
		Lookup:     net.DefaultResolver.LookupHost,
		PrivateDir: fsperm.PrivateDir,
		Owner:      fsperm.Owner,
		Firefox:    firefoxInstalled,
	}, nil
}

// noAdminRights reports whether this account is a standard user: its token is neither elevated nor
// the limited half of an administrator's split token (whose elevation type is "limited"). A prompt
// for such an account asks for another account's credentials. If Windows does not say, it is assumed
// that the prompt can work.
func noAdminRights() bool {
	token := windows.GetCurrentProcessToken()
	if token.IsElevated() {
		return false
	}
	var kind, n uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&kind)), uint32(unsafe.Sizeof(kind)), &n); err != nil {
		return false
	}
	const limited = 3 // TokenElevationTypeLimited
	return kind != limited
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
