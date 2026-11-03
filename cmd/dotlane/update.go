package main

import (
	"encoding/json"
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
		built = builtVersion(info)
	}
	return updateCommand(runtime.GOOS, exe, version, built, hintFS{
		exists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		writable: writable,
		read: func(path string) []byte {
			data, _ := os.ReadFile(path)
			return data
		},
	})
}

// builtVersion is the version Go recorded for the main module, or "" for a build in a clone.
// Since Go 1.24 a clone at a clean release tag records that tag as `go install` would, but only a
// build in a clone records version control settings.
func builtVersion(info *debug.BuildInfo) string {
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return ""
		}
	}
	return info.Main.Version
}

// hintFS is what updateCommand looks at on disk; tests fake it.
type hintFS struct {
	exists   func(path string) bool
	writable func(dir string) bool // whether this user can write in dir
	read     func(path string) []byte
}

// updateCommand tells the install from where the binary is and how it was built: a JavaScript
// package manager keeps it in a node_modules folder (a global one, a project's, or the cache of
// npx or bunx), Homebrew in its Caskroom, a release build has its version set by -ldflags
// (released), `go install` stamps a release version (built), and a build in a clone has none
// (builtVersion). The commands run in any shell, Windows PowerShell 5.1 and cmd.exe too, so they
// take a folder as a flag instead of `cd dir && …`.
func updateCommand(goos, exe, released, built string, fs hintFS) string {
	p := filepath.ToSlash(exe)
	if i := strings.Index(p, "/node_modules/"); i >= 0 {
		root := filepath.FromSlash(p[:i])
		switch {
		case strings.Contains(p, "/_npx/"):
			return "npx dotlane@latest"
		case strings.Contains(p, "/bunx-"):
			return "bunx dotlane@latest"
		// Volta keeps each tool in a folder of its own, under .volta on macOS and Linux and Volta on
		// Windows.
		case strings.Contains(strings.ToLower(p), "volta/tools/image/packages/"):
			return "volta install dotlane@latest"
		// The global folders of pnpm, yarn and bun hold a package.json too, so they come before a
		// project.
		case strings.Contains(p, "/pnpm/global/"):
			return "pnpm add -g dotlane@latest"
		case strings.Contains(p, "/yarn/global/"):
			return "yarn global add dotlane@latest"
		case strings.Contains(p, "/.bun/install/global/"):
			return "bun add -g dotlane@latest"
		case fs.exists(filepath.Join(root, "package.json")):
			return projectCommand(root, fs)
		}
		// A global folder only root can write to, such as /usr/local/lib/node_modules with the
		// installer from nodejs.org, needs sudo; Windows has none.
		if goos != "windows" && !fs.writable(filepath.Join(root, "node_modules")) {
			return "sudo npm install -g dotlane"
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
	bin := filepath.Dir(exe)
	if clone := filepath.Dir(bin); filepath.Base(bin) == "bin" && fs.exists(filepath.Join(clone, "go.mod")) {
		in = " -C " + quote(clone)
	}
	hint := "git" + in + " pull" + then + "go build" + in + " -o " + output + " " + pkg
	if in == "" {
		hint += ", in your clone"
	}
	return hint
}

// projectCommand updates dotlane in the project at root, with the package manager its lockfile
// names. In a monorepo the dependency belongs to one of its workspaces, which the hint cannot
// know, so it leaves that name for the user to fill in.
func projectCommand(root string, fs hintFS) string {
	dir := quote(root)
	var manifest struct {
		Workspaces json.RawMessage `json:"workspaces"`
	}
	json.Unmarshal(fs.read(filepath.Join(root, "package.json")), &manifest)
	workspaces := manifest.Workspaces != nil || fs.exists(filepath.Join(root, "pnpm-workspace.yaml"))
	const where = ", where <workspace> is the one that uses dotlane"
	switch {
	case fs.exists(filepath.Join(root, "bun.lock")) || fs.exists(filepath.Join(root, "bun.lockb")):
		if workspaces {
			return "bun add --cwd <workspace folder> dotlane@latest, where <workspace folder> is the one in " +
				dir + " that uses dotlane"
		}
		return "bun add --cwd " + dir + " dotlane@latest"
	case fs.exists(filepath.Join(root, "pnpm-lock.yaml")):
		if workspaces {
			return "pnpm -C " + dir + " --filter <workspace> add dotlane@latest" + where
		}
		return "pnpm -C " + dir + " add dotlane@latest"
	case fs.exists(filepath.Join(root, "yarn.lock")):
		if workspaces {
			return "yarn --cwd " + dir + " workspace <workspace> add dotlane@latest" + where
		}
		return "yarn --cwd " + dir + " add dotlane@latest"
	}
	if workspaces {
		return "npm install --prefix " + dir + " -w <workspace> dotlane@latest" + where
	}
	return "npm install --prefix " + dir + " dotlane@latest"
}

// quote puts a path with spaces in double quotes, which every shell reads as one argument.
func quote(path string) string {
	if strings.ContainsAny(path, " \t") {
		return `"` + path + `"`
	}
	return path
}
