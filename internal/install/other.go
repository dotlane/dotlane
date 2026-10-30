//go:build !darwin

package install

import (
	"context"
	"fmt"
	"runtime"

	"github.com/pedrodee/dotlane/internal/api"
	"github.com/pedrodee/dotlane/internal/config"
)

// New reports that installing is not supported yet; Linux and Windows come in Phase 4.
func New(cfg *config.Config, configPath string, daemon func(context.Context) (api.Status, error)) (Installer, error) {
	return nil, fmt.Errorf("dotlane install does not support %s yet (macOS only for now)", runtime.GOOS)
}
