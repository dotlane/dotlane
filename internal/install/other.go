//go:build !darwin && !linux && !windows

package install

import (
	"context"
	"fmt"
	"runtime"

	"github.com/dotlane/dotlane/internal/api"
	"github.com/dotlane/dotlane/internal/config"
)

// New reports that installing is not supported on this system.
func New(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (Installer, error) {
	return nil, fmt.Errorf("dotlane install does not support %s yet (macOS, Linux and Windows only)", runtime.GOOS)
}
