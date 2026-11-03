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

// The update hint follows the install: where the binary is, and how it was built. {dir} in a
// want is the folder in this OS's form; the commands must run in any shell, Windows PowerShell 5.1
// included.
func TestUpdateCommand(t *testing.T) {
	files := map[string]bool{
		"/Users/me/app/package.json":                   true,
		"/Users/me/bun app/package.json":               true,
		"/Users/me/bun app/bun.lock":                   true,
		"/Users/me/pnpm-app/package.json":              true,
		"/Users/me/pnpm-app/pnpm-lock.yaml":            true,
		"/Users/me/yarn-app/package.json":              true,
		"/Users/me/yarn-app/yarn.lock":                 true,
		"/Users/me/Library/pnpm/global/5/package.json": true,
		"/Users/me/.config/yarn/global/package.json":   true,
		"/Users/me/.bun/install/global/package.json":   true,
		"/Users/me/src/dotlane/go.mod":                 true,
		"/Users/me/My Projects/dotlane/go.mod":         true,
	}
	exists := func(p string) bool { return files[filepath.ToSlash(p)] }
	for _, tc := range []struct {
		name, goos, exe, released, built, dir, want string
	}{
		{name: "npm global", exe: "/opt/homebrew/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm global, nvm", exe: "/Users/me/.nvm/versions/node/v22.1.0/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm global, Windows", goos: "windows", exe: "C:/Users/me/AppData/Roaming/npm/node_modules/@dotlane/win32-x64/bin/dotlane.exe", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm project", exe: "/Users/me/app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/app", want: "npm install --prefix {dir} dotlane@latest"},
		{name: "bun project, space in path", exe: "/Users/me/bun app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/bun app", want: `bun add --cwd "{dir}" dotlane@latest`},
		{name: "pnpm project", exe: "/Users/me/pnpm-app/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/pnpm-app", want: "pnpm -C {dir} add dotlane@latest"},
		{name: "yarn project", exe: "/Users/me/yarn-app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/yarn-app", want: "yarn --cwd {dir} add dotlane@latest"},
		{name: "npx", exe: "/Users/me/.npm/_npx/1a2b3c/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npx dotlane@latest"},
		{name: "bunx", exe: "/private/var/folders/xy/T/bunx-501-dotlane@latest/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "bunx dotlane@latest"},
		{name: "pnpm global", exe: "/Users/me/Library/pnpm/global/5/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "pnpm add -g dotlane@latest"},
		{name: "yarn global", exe: "/Users/me/.config/yarn/global/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "yarn global add dotlane@latest"},
		{name: "bun global", exe: "/Users/me/.bun/install/global/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "bun add -g dotlane@latest"},
		{name: "Homebrew cask", exe: "/opt/homebrew/Caskroom/dotlane/0.2.0/dotlane", released: "0.2.0", want: "brew upgrade dotlane"},
		{name: "Homebrew formula", exe: "/home/linuxbrew/.linuxbrew/Cellar/dotlane/0.2.0/bin/dotlane", released: "0.2.0", want: "brew upgrade dotlane"},
		{name: "release archive", exe: "/usr/local/bin/dotlane", released: "0.2.0", want: "get the latest release from https://github.com/dotlane/dotlane/releases/latest"},
		{name: "go install", exe: "/Users/me/go/bin/dotlane", built: "v0.2.0", want: "go install github.com/dotlane/dotlane/cmd/dotlane@latest"},
		{name: "clone", exe: "/Users/me/src/dotlane/bin/dotlane", built: "v0.0.0-20261103101010-abcdefabcdef+dirty", dir: "/Users/me/src/dotlane",
			want: "git -C {dir} pull && go build -C {dir} -o bin/dotlane ./cmd/dotlane"},
		{name: "clone, Windows", goos: "windows", exe: "/Users/me/My Projects/dotlane/bin/dotlane.exe", built: "(devel)", dir: "/Users/me/My Projects/dotlane",
			want: `git -C "{dir}" pull, then go build -C "{dir}" -o bin\dotlane.exe .\cmd\dotlane`},
		{name: "clone, elsewhere", exe: "/tmp/dotlane", built: "(devel)", want: "git pull && go build -o bin/dotlane ./cmd/dotlane, in your clone"},
		{name: "clone, elsewhere, Windows", goos: "windows", exe: "/tmp/dotlane.exe", want: `git pull, then go build -o bin\dotlane.exe .\cmd\dotlane, in your clone`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := strings.ReplaceAll(tc.want, "{dir}", filepath.FromSlash(tc.dir))
			if got := updateCommand(tc.goos, filepath.FromSlash(tc.exe), tc.released, tc.built, exists); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			if tc.goos == "windows" && strings.Contains(want, "&&") {
				t.Errorf("Windows PowerShell 5.1 has no &&: %q", want)
			}
		})
	}
}
