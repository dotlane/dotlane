//go:build windows

package install

import (
	"context"
	"errors"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
)

// New returns the installer for this machine.
func New(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (Installer, error) {
	return nil, errors.New("dotlane install on windows: not implemented yet")
}
