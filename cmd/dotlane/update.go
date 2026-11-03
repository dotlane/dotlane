package main

import (
	"os"
	"path/filepath"
	"runtime"
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
	return updateCommand(runtime.GOOS, exe, version, built, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

// updateCommand tells the install from where the binary is and how it was built: a JavaScript
// package manager keeps it in a node_modules folder (a global one, a project's, or the cache of
// npx or bunx), Homebrew in its Caskroom, a release build has its version set by -ldflags
// (released), `go install` stamps a release version and a build in a clone a pseudo-version or
// none. exists reports whether a file exists. The commands run in any shell, Windows PowerShell
// 5.1 and cmd.exe too, so they take a folder as a flag instead of `cd dir && …`.
func updateCommand(goos, exe, released, built string, exists func(string) bool) string {
	p := filepath.ToSlash(exe)
	if i := strings.Index(p, "/node_modules/"); i >= 0 {
		root := filepath.FromSlash(p[:i])
		switch {
		case strings.Contains(p, "/_npx/"):
			return "npx dotlane@latest"
		case strings.Contains(p, "/bunx-"):
			return "bunx dotlane@latest"
		// The global folders of pnpm, yarn and bun hold a package.json too, so they come before a
		// project.
		case strings.Contains(p, "/pnpm/global/"):
			return "pnpm add -g dotlane@latest"
		case strings.Contains(p, "/yarn/global/"):
			return "yarn global add dotlane@latest"
		case strings.Contains(p, "/.bun/install/global/"):
			return "bun add -g dotlane@latest"
		case exists(filepath.Join(root, "package.json")):
			// A project's lockfile names its package manager.
			dir := quote(root)
			switch {
			case exists(filepath.Join(root, "bun.lock")) || exists(filepath.Join(root, "bun.lockb")):
				return "bun add --cwd " + dir + " dotlane@latest"
			case exists(filepath.Join(root, "pnpm-lock.yaml")):
				return "pnpm -C " + dir + " add dotlane@latest"
			case exists(filepath.Join(root, "yarn.lock")):
				return "yarn --cwd " + dir + " add dotlane@latest"
			}
			return "npm install --prefix " + dir + " dotlane@latest"
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
	// git -C and go -C run in the clone without a cd. Windows PowerShell 5.1 has no &&, and cmd.exe
	// no ;, so there the two commands are given one after the other.
	output, pkg, then := "bin/dotlane", "./cmd/dotlane", " && "
	if goos == "windows" {
		output, pkg, then = `bin\dotlane.exe`, `.\cmd\dotlane`, ", then "
	}
	in := ""
	if dir := filepath.Dir(exe); filepath.Base(dir) == "bin" && exists(filepath.Join(filepath.Dir(dir), "go.mod")) {
		in = " -C " + quote(filepath.Dir(dir))
	}
	hint := "git" + in + " pull" + then + "go build" + in + " -o " + output + " " + pkg
	if in == "" {
		hint += ", in your clone"
	}
	return hint
}

// quote puts a path with spaces in double quotes, which every shell reads as one argument.
func quote(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}
