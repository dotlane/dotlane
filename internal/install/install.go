// Package install sets Dotlane up as a login service, and removes it again.
//
// Each step checks the current state first, so install and uninstall can be re-run safely. Steps
// that need administrator rights run through sudo, one explained command at a time, so the user
// can see exactly what is being changed.
package install

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// Installer sets Dotlane up on one operating system, checks it, and removes it.
type Installer interface {
	Install(ctx context.Context) error
	// Refresh updates an installed login service to this binary and restarts it if it runs an
	// older one, without administrator rights; it does nothing when Dotlane is not installed.
	// Package managers run it after an upgrade.
	Refresh(ctx context.Context) error
	// Upgrade refreshes an installed login service when its daemon runs an older release than
	// version (see Newer), reporting to out, and says whether it did. Package managers replace
	// the binary on the PATH but cannot restart the service (Homebrew runs cask hooks in a
	// sandbox, and the npm packages have no install scripts), so commands that use the daemon
	// call it first.
	Upgrade(ctx context.Context, version string, out io.Writer) (bool, error)
	// Uninstall reverses Install; purge also deletes Dotlane's state and log.
	Uninstall(ctx context.Context, purge bool) error
	// Doctor checks the installation and what could get in its way.
	Doctor(ctx context.Context) []Check
	// Migrate moves the machine from the Bash prototype to Dotlane, keeping its sites and CA.
	Migrate(ctx context.Context) error
}

// Newer reports whether version is a later release than running, the daemon's version. Both must
// be semantic versions ("0.2.0", "v0.2.0-rc.1"). A development build ("dev", or a Go
// pseudo-version such as "v0.0.0-20261030172955-9c941e1f9f7e") never counts on either side, so
// a local build and an installed release do not replace each other.
func Newer(version, running string) bool {
	v, r := canonical(version), canonical(running)
	return v != "" && r != "" && semver.Compare(v, r) > 0
}

// canonical is version with its "v", or "" for anything but a release's semantic version.
func canonical(version string) string {
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) || module.IsPseudoVersion(version) {
		return ""
	}
	return version
}

// CheckStatus is the outcome of one check.
type CheckStatus string

const (
	Pass    CheckStatus = "ok"
	Warning CheckStatus = "warn"
	Failed  CheckStatus = "fail"
	Skipped CheckStatus = "skip"
)

// Check is one line of dotlane doctor.
type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
	Fix    string      `json:"fix,omitempty"`
	// Repairable means dotlane install fixes it (doctor --fix runs it).
	Repairable bool `json:"repairable"`
	// Conflict means another tool is in the way; Dotlane leaves other tools alone, so the user
	// has to act first.
	Conflict bool `json:"conflict"`
}

// Runner runs the commands install needs. Tests replace it; nothing else should.
type Runner interface {
	// Run runs an unprivileged command and returns its combined output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// Sudo runs a command as root. stdin, if not nil, is fed to the command.
	Sudo(ctx context.Context, stdin []byte, name string, args ...string) error
}

// ExecRunner runs real commands; sudo asks for the password on the terminal.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Sudo implements Runner. sudo reads the password from the terminal, not from stdin, so stdin
// stays free for the command's input.
func (ExecRunner) Sudo(ctx context.Context, stdin []byte, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", append([]string{name}, args...)...)
	if stdin != nil {
		cmd.Stdin = strings.NewReader(string(stdin))
		cmd.Stdout = io.Discard // tee echoes its input
	} else {
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
