package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/dotlane/dotlane/internal/config"
	"github.com/dotlane/dotlane/internal/fsperm"
)

// serviceLockWait is how long a command waits for another one to finish with the login service.
const serviceLockWait = time.Minute

// lockService takes the lock that lets one command at a time change the login service: after an
// upgrade, commands started together (a dev script running `dotlane up` in each package) all find
// the old daemon, and without it each would restart the service over the others. The next one
// waits, then finds the daemon up to date. Install, and so doctor --fix and migrate, holds it too,
// so an automatic upgrade does not restart the service in the middle of one. It gives up when ctx ends or wait passes, and returns
// the function that releases the lock; the OS releases it too if the command dies.
func lockService(ctx context.Context, cfg *config.Config, wait time.Duration) (func(), error) {
	// install takes the lock before the daemon has made its state folder; make it as the daemon
	// would, private, and leave an existing one alone.
	if _, err := os.Stat(cfg.StateDir); err != nil {
		if err := fsperm.PrivateDir(cfg.StateDir); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(filepath.Join(cfg.StateDir, "service.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		locked, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		if locked {
			return func() { f.Close() }, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, errors.New("another dotlane command is still updating the login service")
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
