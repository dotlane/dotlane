package main

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime/debug"
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
	files := map[string]string{
		"/Users/me/app/package.json":                   `{"name": "app"}`,
		"/Users/me/bun app/package.json":               `{"name": "app"}`,
		"/Users/me/bun app/bun.lock":                   "",
		"/Users/me/pnpm-app/package.json":              `{"name": "app"}`,
		"/Users/me/pnpm-app/pnpm-lock.yaml":            "",
		"/Users/me/yarn-app/package.json":              `{"name": "app"}`,
		"/Users/me/yarn-app/yarn.lock":                 "",
		"/Users/me/mono/package.json":                  `{"name": "mono", "workspaces": ["packages/*"]}`,
		"/Users/me/pnpm-mono/package.json":             `{"name": "mono"}`,
		"/Users/me/pnpm-mono/pnpm-lock.yaml":           "",
		"/Users/me/pnpm-mono/pnpm-workspace.yaml":      "",
		"/Users/me/yarn-mono/package.json":             `{"workspaces": {"packages": ["apps/*"]}}`,
		"/Users/me/yarn-mono/yarn.lock":                "",
		"/Users/me/bun-mono/package.json":              `{"workspaces": ["apps/*"]}`,
		"/Users/me/bun-mono/bun.lockb":                 "",
		"/Users/me/Library/pnpm/global/5/package.json": "{}",
		"/Users/me/.config/yarn/global/package.json":   "{}",
		"/Users/me/.bun/install/global/package.json":   "{}",
		"/Users/me/src/dotlane/go.mod":                 "",
		"/Users/me/My Projects/dotlane/go.mod":         "",
	}
	readOnly := map[string]bool{"/usr/local/lib/node_modules": true}
	fs := hintFS{
		exists: func(p string) bool {
			_, ok := files[filepath.ToSlash(p)]
			return ok
		},
		writable: func(p string) bool { return !readOnly[filepath.ToSlash(p)] },
		read:     func(p string) []byte { return []byte(files[filepath.ToSlash(p)]) },
	}
	const where = ", where <workspace> is the one that uses dotlane"
	for _, tc := range []struct {
		name, goos, exe, released, built, dir, want string
	}{
		{name: "npm global", exe: "/opt/homebrew/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm global, nvm", exe: "/Users/me/.nvm/versions/node/v22.1.0/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm global, owned by root", exe: "/usr/local/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "sudo npm install -g dotlane"},
		{name: "npm global, Windows", goos: "windows", exe: "C:/Users/me/AppData/Roaming/npm/node_modules/@dotlane/win32-x64/bin/dotlane.exe", released: "0.2.0", want: "npm install -g dotlane"},
		{name: "npm project", exe: "/Users/me/app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/app", want: "npm install --prefix {dir} dotlane@latest"},
		{name: "bun project, space in path", exe: "/Users/me/bun app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/bun app", want: `bun add --cwd "{dir}" dotlane@latest`},
		{name: "pnpm project", exe: "/Users/me/pnpm-app/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/pnpm-app", want: "pnpm -C {dir} add dotlane@latest"},
		{name: "yarn project", exe: "/Users/me/yarn-app/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/yarn-app", want: "yarn --cwd {dir} add dotlane@latest"},
		{name: "npm workspaces", exe: "/Users/me/mono/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/mono", want: "npm install --prefix {dir} -w <workspace> dotlane@latest" + where},
		{name: "pnpm workspaces", exe: "/Users/me/pnpm-mono/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/pnpm-mono", want: "pnpm -C {dir} --filter <workspace> add dotlane@latest" + where},
		{name: "yarn workspaces", exe: "/Users/me/yarn-mono/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/yarn-mono", want: "yarn --cwd {dir} workspace <workspace> add dotlane@latest" + where},
		{name: "bun workspaces", exe: "/Users/me/bun-mono/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", dir: "/Users/me/bun-mono", want: "bun add --cwd <workspace folder> dotlane@latest, where <workspace folder> is the one in {dir} that uses dotlane"},
		{name: "npx", exe: "/Users/me/.npm/_npx/1a2b3c/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "npx dotlane@latest"},
		{name: "bunx", exe: "/private/var/folders/xy/T/bunx-501-dotlane@latest/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "bunx dotlane@latest"},
		{name: "Volta", exe: "/Users/me/.volta/tools/image/packages/dotlane/lib/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "volta install dotlane@latest"},
		{name: "Volta, Windows", goos: "windows", exe: "C:/Users/me/AppData/Local/Volta/tools/image/packages/dotlane/node_modules/@dotlane/win32-x64/bin/dotlane.exe", released: "0.2.0", want: "volta install dotlane@latest"},
		{name: "pnpm global", exe: "/Users/me/Library/pnpm/global/5/node_modules/.pnpm/@dotlane+darwin-arm64@0.2.0/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "pnpm add -g dotlane@latest"},
		{name: "yarn global", exe: "/Users/me/.config/yarn/global/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "yarn global add dotlane@latest"},
		{name: "bun global", exe: "/Users/me/.bun/install/global/node_modules/@dotlane/darwin-arm64/bin/dotlane", released: "0.2.0", want: "bun add -g dotlane@latest"},
		{name: "Homebrew cask", exe: "/opt/homebrew/Caskroom/dotlane/0.2.0/dotlane", released: "0.2.0", want: "brew upgrade dotlane"},
		{name: "Homebrew formula", exe: "/home/linuxbrew/.linuxbrew/Cellar/dotlane/0.2.0/bin/dotlane", released: "0.2.0", want: "brew upgrade dotlane"},
		{name: "release archive", exe: "/usr/local/bin/dotlane", released: "0.2.0", want: "get the latest release from https://github.com/dotlane/dotlane/releases/latest"},
		{name: "go install", exe: "/Users/me/go/bin/dotlane", built: "v0.2.0", want: "go install github.com/dotlane/dotlane/cmd/dotlane@latest"},
		{name: "clone", exe: "/Users/me/src/dotlane/bin/dotlane", built: "v0.0.0-20261103101010-abcdefabcdef+dirty", dir: "/Users/me/src/dotlane",
			want: "git -C {dir} pull && go build -C {dir} -o bin/dotlane ./cmd/dotlane"},
		{name: "clone at a release tag", exe: "/Users/me/src/dotlane/bin/dotlane", dir: "/Users/me/src/dotlane", // builtVersion gives ""
			want: "git -C {dir} pull && go build -C {dir} -o bin/dotlane ./cmd/dotlane"},
		{name: "clone, Windows", goos: "windows", exe: "/Users/me/My Projects/dotlane/bin/dotlane.exe", built: "(devel)", dir: "/Users/me/My Projects/dotlane",
			want: `git -C "{dir}" pull, then go build -C "{dir}" -o bin\dotlane.exe .\cmd\dotlane`},
		{name: "clone, elsewhere", exe: "/tmp/dotlane", built: "(devel)", want: "git pull && go build -o bin/dotlane ./cmd/dotlane, in your clone"},
		{name: "clone, elsewhere, Windows", goos: "windows", exe: "/tmp/dotlane.exe", want: `git pull, then go build -o bin\dotlane.exe .\cmd\dotlane, in your clone`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := strings.ReplaceAll(tc.want, "{dir}", filepath.FromSlash(tc.dir))
			if got := updateCommand(tc.goos, filepath.FromSlash(tc.exe), tc.released, tc.built, fs); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
			if tc.goos == "windows" && strings.Contains(want, "&&") {
				t.Errorf("Windows PowerShell 5.1 has no &&: %q", want)
			}
		})
	}
}

// Since Go 1.24 a build in a clone at a clean release tag records that tag, like go install; only
// the version control settings tell them apart.
func TestBuiltVersion(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}}
	if got := builtVersion(installed); got != "v0.2.0" {
		t.Errorf("go install: %q", got)
	}
	cloned := &debug.BuildInfo{Main: debug.Module{Version: "v0.2.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs", Value: "git"}, {Key: "vcs.revision", Value: "fede79627908"}, {Key: "vcs.modified", Value: "false"},
	}}
	if got := builtVersion(cloned); got != "" {
		t.Errorf("clone at a tag: %q, want none", got)
	}
}
