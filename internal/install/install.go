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
)

// Installer sets Dotlane up on one operating system, checks it, and removes it.
type Installer interface {
	Install(ctx context.Context) error
	// Uninstall reverses Install; purge also deletes Dotlane's state and log.
	Uninstall(ctx context.Context, purge bool) error
	// Doctor checks the installation and what could get in its way.
	Doctor(ctx context.Context) []Check
	// Migrate moves the machine from the Bash prototype to Dotlane, keeping its sites and CA.
	Migrate(ctx context.Context) error
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
