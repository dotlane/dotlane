// Package php runs PHP sites: it finds the PHP runtimes installed on this machine (PHP-FPM from
// Homebrew, mise, asdf or the system's packages, and FrankenPHP), and supervises them, one
// PHP-FPM pool per PHP version and one FrankenPHP process per site. Dotlane's Caddy talks to
// each over a Unix socket in the state directory.
package php

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dotlane/dotlane/internal/sites"
)

// Install is one PHP runtime found on this machine.
type Install struct {
	Runtime    string // sites.RuntimeFPM or sites.RuntimeFrankenPHP
	PHPVersion string // the full PHP version, such as 8.4.13
	// Version is FrankenPHP's own version (1.13.4); empty for PHP-FPM.
	Version string
	Binary  string
	Source  string // where it was found: Homebrew, mise, asdf, system, PATH, Dotlane
}

// Minor is the PHP version's major.minor, as sites ask for it.
func (i Install) Minor() string {
	parts := strings.SplitN(i.PHPVersion, ".", 3)
	if len(parts) < 2 {
		return i.PHPVersion
	}
	return parts[0] + "." + parts[1]
}

// String names the install for messages: "PHP 8.4.13 (Homebrew)", "FrankenPHP 1.13.4 with PHP
// 8.5.12 (Dotlane)".
func (i Install) String() string {
	if i.Runtime == sites.RuntimeFrankenPHP {
		return fmt.Sprintf("FrankenPHP %s with PHP %s (%s)", i.Version, i.PHPVersion, i.Source)
	}
	return fmt.Sprintf("PHP %s (%s)", i.PHPVersion, i.Source)
}

// place is a glob where a runtime may be installed.
type place struct {
	runtime, pattern, source string
}

// places are where Find looks, in order of preference for the same version. The login service
// starts with a short PATH, so the usual install folders are listed rather than found on PATH.
func places(home, stateDir string) []place {
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	ps := []place{{sites.RuntimeFrankenPHP, filepath.Join(stateDir, "frankenphp", "*", "frankenphp"+exe), "Dotlane"}}
	if runtime.GOOS != "windows" { // PHP-FPM does not exist on Windows
		mise := os.Getenv("MISE_DATA_DIR")
		if mise == "" {
			mise = filepath.Join(home, ".local", "share", "mise")
		}
		for _, brew := range []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"} {
			ps = append(ps,
				place{sites.RuntimeFPM, brew + "/opt/php*/sbin/php-fpm", "Homebrew"},
				place{sites.RuntimeFrankenPHP, brew + "/bin/frankenphp", "Homebrew"})
		}
		ps = append(ps,
			place{sites.RuntimeFPM, filepath.Join(mise, "installs", "php", "*", "sbin", "php-fpm"), "mise"},
			place{sites.RuntimeFPM, filepath.Join(home, ".asdf", "installs", "php", "*", "sbin", "php-fpm"), "asdf"},
			// Debian and Ubuntu name it php-fpm8.3; Fedora and Arch, php-fpm.
			place{sites.RuntimeFPM, "/usr/sbin/php-fpm*", "system"},
			place{sites.RuntimeFPM, "/usr/bin/php-fpm*", "system"},
			place{sites.RuntimeFPM, "/usr/local/sbin/php-fpm*", "system"},
			place{sites.RuntimeFrankenPHP, "/usr/bin/frankenphp", "system"},
			place{sites.RuntimeFrankenPHP, filepath.Join(home, ".local", "bin", "frankenphp"), "PATH"},
		)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir != "" && filepath.IsAbs(dir) {
			if runtime.GOOS != "windows" {
				ps = append(ps, place{sites.RuntimeFPM, filepath.Join(dir, "php-fpm"), "PATH"})
			}
			ps = append(ps, place{sites.RuntimeFrankenPHP, filepath.Join(dir, "frankenphp"+exe), "PATH"})
		}
	}
	return ps
}

// Find lists the PHP runtimes installed on this machine, newest PHP first. stateDir holds the
// FrankenPHP that dotlane frankenphp install downloads.
func Find(ctx context.Context, stateDir string) []Install {
	home, _ := os.UserHomeDir()
	return find(ctx, places(home, stateDir))
}

func find(ctx context.Context, ps []place) []Install {
	var out []Install
	seen := map[string]bool{} // Homebrew's opt/php and opt/php@8.5 can be the same binary
	for _, p := range ps {
		matches, _ := filepath.Glob(p.pattern)
		for _, path := range matches {
			real, err := filepath.EvalSymlinks(path)
			if err != nil || seen[real] {
				continue
			}
			if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() || (runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
				continue
			}
			seen[real] = true
			if i, ok := probe(ctx, p.runtime, path); ok {
				i.Source = p.source
				out = append(out, i)
			}
		}
	}
	// Newest PHP first; for the same version, the place listed first.
	slices.SortStableFunc(out, func(a, b Install) int { return compareVersions(b.PHPVersion, a.PHPVersion) })
	return out
}

var (
	fpmVersion        = regexp.MustCompile(`^PHP ([0-9]+\.[0-9]+\.[0-9]+)\S* \(fpm-fcgi\)`)
	frankenPHPVersion = regexp.MustCompile(`^FrankenPHP v?(\S+) PHP ([0-9]+\.[0-9]+\.[0-9]+)`)
)

// probes caches what each binary reported, by its path, size and modification time, so the
// daemon does not run every PHP on each change.
var probes = struct {
	sync.Mutex
	m map[string]Install
}{m: map[string]Install{}}

// probe runs the binary to read its versions (php-fpm -v, frankenphp version).
func probe(ctx context.Context, rt, path string) (Install, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return Install{}, false
	}
	key := fmt.Sprintf("%s\x00%s\x00%d\x00%d", rt, path, info.Size(), info.ModTime().UnixNano())
	probes.Lock()
	cached, ok := probes.m[key]
	probes.Unlock()
	if ok {
		return cached, cached.PHPVersion != ""
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	i := Install{Runtime: rt, Binary: path}
	if rt == sites.RuntimeFPM {
		out, _ := exec.CommandContext(ctx, path, "-v").Output()
		if m := fpmVersion.FindSubmatch(out); m != nil {
			i.PHPVersion = string(m[1])
		}
	} else {
		out, _ := exec.CommandContext(ctx, path, "version").Output()
		if m := frankenPHPVersion.FindSubmatch(out); m != nil {
			i.Version, i.PHPVersion = string(m[1]), string(m[2])
		}
	}
	if ctx.Err() != nil {
		return Install{}, false // a slow machine; try again next time
	}
	probes.Lock()
	probes.m[key] = i
	probes.Unlock()
	return i, i.PHPVersion != ""
}

// compareVersions compares dotted numeric versions: negative when a is older than b.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x - y
		}
	}
	return 0
}

// NotFoundError says that no install fits a site, with what there is and the fix.
type NotFoundError struct{ msg string }

func (e *NotFoundError) Error() string { return e.msg }

// Choose picks the install that serves a site: for PHP-FPM, the newest PHP of the major.minor
// the site asks for, or the newest of all for sites.LatestPHP; for FrankenPHP, the newest
// FrankenPHP whose PHP fits. installs come from Find, newest first.
func Choose(installs []Install, p sites.PHP) (Install, error) {
	rt := p.RuntimeOrDefault()
	var best Install
	var found []Install
	for _, i := range installs {
		if i.Runtime != rt {
			continue
		}
		found = append(found, i)
		if p.Version != sites.LatestPHP && i.Minor() != p.Version {
			continue
		}
		// installs are newest PHP first; FrankenPHP's own version decides between its builds.
		if best.Binary == "" || (rt == sites.RuntimeFrankenPHP && compareVersions(i.Version, best.Version) > 0) {
			best = i
		}
	}
	if best.Binary == "" {
		return Install{}, &NotFoundError{notFound(rt, p.Version, found)}
	}
	return best, nil
}

func notFound(rt, version string, found []Install) string {
	var b strings.Builder
	if rt == sites.RuntimeFrankenPHP {
		if len(found) == 0 {
			b.WriteString("FrankenPHP is not installed\ninstall it: dotlane frankenphp install")
			return b.String()
		}
		fmt.Fprintf(&b, "no FrankenPHP here comes with PHP %s; each FrankenPHP build brings one PHP version\nfound: %s", version, list(found))
		fmt.Fprintf(&b, "\nuse php = %q or %q, or PHP-FPM (runtime = %q) for PHP %s", found[0].Minor(), sites.LatestPHP, sites.RuntimeFPM, version)
		return b.String()
	}
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("PHP-FPM does not exist on Windows; use FrankenPHP: runtime = %q (dotlane add --frankenphp)", sites.RuntimeFrankenPHP)
	}
	if version == sites.LatestPHP {
		b.WriteString("no PHP with PHP-FPM is installed")
	} else {
		fmt.Fprintf(&b, "PHP %s with PHP-FPM is not installed", version)
	}
	if len(found) > 0 {
		fmt.Fprintf(&b, "\nfound: %s", list(found))
	}
	b.WriteString("\ninstall it: " + installHint(version))
	return b.String()
}

func list(installs []Install) string {
	names := make([]string, len(installs))
	for i, in := range installs {
		names[i] = in.String()
	}
	return strings.Join(names, ", ")
}

// installHint is the usual command that installs PHP-FPM for a PHP version on this OS.
func installHint(version string) string {
	if runtime.GOOS == "darwin" {
		if version == sites.LatestPHP {
			return "brew install php"
		}
		return "brew install php@" + version + "   (or: mise use -g php@" + version + ")"
	}
	if version == sites.LatestPHP {
		return "your package manager's php-fpm package (php-fpm, php8.4-fpm), or mise use -g php"
	}
	return "your package manager's PHP-FPM package for " + version + " (php" + version + "-fpm on Debian and Ubuntu), or mise use -g php@" + version
}
