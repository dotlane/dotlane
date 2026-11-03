package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/fang/v2"

	"github.com/dotlane/dotlane/internal/api"
)

// Every command's help renders through Fang. Fang parses examples as shell commands, and an
// example it cannot parse made `dotlane up --help` panic.
func TestHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, c := range newRoot().Commands() {
		if c.Hidden {
			continue
		}
		name := c.Name()
		t.Run(name, func(t *testing.T) {
			root := newRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{name, "--help"})
			if err := fang.Execute(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "USAGE") {
				t.Errorf("no usage:\n%s", out.String())
			}
		})
	}
}

func TestUsageHint(t *testing.T) {
	err := withUsageHint(errorString("accepts 2 arg(s), received 1"))
	if !strings.HasSuffix(err.Error(), "try --help for usage") {
		t.Errorf("got %q", err)
	}
	if err := withUsageHint(errorString("no such site: x")); strings.Contains(err.Error(), "--help") {
		t.Errorf("hint on a non-usage error: %q", err)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// An old command on the PATH and a newer service, or the other way round: a different API fails
// and names the side to update; a newer release with the same API only warns.
func TestCompatible(t *testing.T) {
	for _, tc := range []struct {
		name        string
		st          api.Status
		cli         string
		warn, fails string
	}{
		{name: "same", st: api.Status{Version: "0.2.0", API: api.Version}, cli: "0.2.0"},
		{name: "older service", st: api.Status{Version: "0.1.0", API: api.Version}, cli: "0.2.0"},
		{name: "service from before api", st: api.Status{Version: "0.1.0"}, cli: "0.2.0"},
		{name: "dev build", st: api.Status{Version: "0.3.0", API: api.Version}, cli: "dev"},
		{name: "newer service", st: api.Status{Version: "0.3.0", API: api.Version}, cli: "0.2.0", warn: "older than the service (0.3.0); update it: brew upgrade dotlane"},
		{name: "newer api", st: api.Status{Version: "0.3.0", API: api.Version + 1}, cli: "0.2.0", fails: "update it: brew upgrade dotlane"},
		{name: "older api", st: api.Status{Version: "0.3.0", API: -1}, cli: "dev", fails: "dotlane install --refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := compatible(tc.st, tc.cli, func() string { return "brew upgrade dotlane" })
			if tc.fails != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fails) {
					t.Errorf("err = %v, want %q", err, tc.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (tc.warn == "") != (warn == "") || !strings.Contains(warn, tc.warn) {
				t.Errorf("warning = %q, want %q", warn, tc.warn)
			}
		})
	}
}

// The update hint follows the install: where the binary is, and how it was built.
func TestUpdateCommand(t *testing.T) {
	files := map[string]bool{
		"/Users/me/app/package.json":                   true,
		"/Users/me/Library/pnpm/global/5/package.json": true,
		"/Users/me/.config/yarn/global/package.json":   true,
		"/Users/me/src/dotlane/go.mod":                 true,
	}
	exists := func(p string) bool { return files[filepath.ToSlash(p)] }
	for _, tc := range []struct {
		name, exe, released, built, want string
	}{
		{"npm global", "/opt/homebrew/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "npm install -g dotlane"},
		{"npm global, nvm", "/Users/me/.nvm/versions/node/v22.1.0/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "npm install -g dotlane"},
		{"npm global, Windows", "C:/Users/me/AppData/Roaming/npm/node_modules/@dotlane/win32-x64/bin/dotlane.exe", "0.2.0", "", "npm install -g dotlane"},
		{"project", "/Users/me/app/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "cd /Users/me/app && npm install dotlane@latest"},
		{"npx", "/Users/me/.npm/_npx/1a2b3c/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "npx dotlane@latest"},
		{"pnpm global", "/Users/me/Library/pnpm/global/5/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "pnpm add -g dotlane@latest"},
		{"yarn global", "/Users/me/.config/yarn/global/node_modules/@dotlane/darwin-arm64/bin/dotlane", "0.2.0", "", "yarn global add dotlane@latest"},
		{"Homebrew cask", "/opt/homebrew/Caskroom/dotlane/0.2.0/dotlane", "0.2.0", "", "brew upgrade dotlane"},
		{"Homebrew formula", "/home/linuxbrew/.linuxbrew/Cellar/dotlane/0.2.0/bin/dotlane", "0.2.0", "", "brew upgrade dotlane"},
		{"release archive", "/usr/local/bin/dotlane", "0.2.0", "", "get the latest release from https://github.com/dotlane/dotlane/releases/latest"},
		{"go install", "/Users/me/go/bin/dotlane", "", "v0.2.0", "go install github.com/dotlane/dotlane/cmd/dotlane@latest"},
		{"clone", "/Users/me/src/dotlane/bin/dotlane", "", "v0.0.0-20261103101010-abcdefabcdef+dirty", "cd /Users/me/src/dotlane && git pull && go build -o bin/dotlane ./cmd/dotlane"},
		{"clone, elsewhere", "/tmp/dotlane", "", "(devel)", "git pull && go build -o bin/dotlane ./cmd/dotlane, in your clone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := filepath.ToSlash(updateCommand(tc.exe, tc.released, tc.built, exists)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
