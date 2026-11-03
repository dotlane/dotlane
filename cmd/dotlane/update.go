package main

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// updateHint is the command that updates this dotlane, for the way it was installed.
func updateHint() string {
	exe, err := os.Executable()
	if err == nil {
		if p, err := filepath.EvalSymlinks(exe); err == nil {
			exe = p
		}
	}
	built := ""
	if info, ok := debug.ReadBuildInfo(); ok {
		built = info.Main.Version
	}
	return updateCommand(exe, version, built, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

// updateCommand tells the install from where the binary is and how it was built: npm keeps it in
// a node_modules folder (a global one, a project's, or npx's cache; pnpm and yarn have their own
// global ones), Homebrew in its Caskroom, a
// release build has its version set by -ldflags (released), `go install` stamps a release
// version and a build in a clone a pseudo-version or none. exists reports whether a file exists.
func updateCommand(exe, released, built string, exists func(string) bool) string {
	p := filepath.ToSlash(exe)
	if i := strings.Index(p, "/node_modules/"); i >= 0 {
		root := filepath.FromSlash(p[:i])
		switch {
		case strings.Contains(p, "/_npx/"):
			return "npx dotlane@latest"
		// pnpm's and yarn's global folders hold a package.json too, so they come before a project.
		case strings.Contains(p, "/pnpm/global/"):
			return "pnpm add -g dotlane@latest"
		case strings.Contains(p, "/yarn/global/"):
			return "yarn global add dotlane@latest"
		case exists(filepath.Join(root, "package.json")):
			return "cd " + root + " && npm install dotlane@latest"
		}
		return "npm install -g dotlane"
	}
	if strings.Contains(p, "/Caskroom/dotlane/") || strings.Contains(p, "/Cellar/dotlane/") {
		return "brew upgrade dotlane"
	}
	if released != "" {
		return "get the latest release from https://github.com/dotlane/dotlane/releases/latest"
	}
	if v := "v" + strings.TrimPrefix(built, "v"); semver.IsValid(v) && !module.IsPseudoVersion(v) {
		return "go install github.com/dotlane/dotlane/cmd/dotlane@latest"
	}
	build := "git pull && go build -o bin/dotlane ./cmd/dotlane"
	if dir := filepath.Dir(exe); filepath.Base(dir) == "bin" && exists(filepath.Join(filepath.Dir(dir), "go.mod")) {
		return "cd " + filepath.Dir(dir) + " && " + build
	}
	return build + ", in your clone"
}
