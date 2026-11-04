// Package sites keeps the registry of sites: a name and the local port it forwards to, or the
// folder of PHP files it serves.
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Site is https://<Name>.<tld> forwarded to localhost:<Port>, or, for a PHP site, served from
// the files in PHP.Root by a PHP runtime Dotlane runs. A site has one or the other.
type Site struct {
	Name string `json:"name"`
	Port int    `json:"port,omitempty"`
	// Project is the directory of the dotlane.toml that registered the site (dotlane up), or
	// empty for one added by hand (dotlane add).
	Project string `json:"project,omitempty"`
	PHP     PHP    `json:"php,omitzero"`
}

// PHP is how a PHP site is served. The zero value means the site is not a PHP site.
type PHP struct {
	Root string `json:"root"` // absolute: the folder served, such as a Laravel app's public/
	// Version is the PHP version asked for: major.minor ("8.4"), or "latest" for the newest
	// one installed.
	Version string `json:"version"`
	// Runtime is RuntimeFPM or RuntimeFrankenPHP; empty means the default (DefaultRuntime).
	Runtime string `json:"runtime,omitempty"`
	// Worker is a script relative to Root that FrankenPHP keeps running in worker mode
	// (Laravel Octane, Symfony Runtime); empty for a script per request.
	Worker string `json:"worker,omitempty"`
}

// The PHP runtimes: PHP-FPM, one pool per PHP version installed on the machine, or FrankenPHP.
const (
	RuntimeFPM        = "fpm"
	RuntimeFrankenPHP = "frankenphp"
)

// LatestPHP asks for the newest PHP version installed.
const LatestPHP = "latest"

// IsPHP reports whether s is served from PHP files rather than forwarded to a port.
func (s Site) IsPHP() bool { return s.PHP.Root != "" }

// ErrNotFound is returned when removing a site that does not exist.
var ErrNotFound = errors.New("no such site")

// InvalidError reports a site that breaks the naming or port rules.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

// Invalid is an InvalidError with msg, for a site the daemon cannot serve as asked.
func Invalid(msg string) *InvalidError { return &InvalidError{msg} }

// ConflictError reports a name another project already uses.
type ConflictError struct {
	Name  string
	Owner string // the other project's directory
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s is already used by the project in %s\nrun dotlane down there, or dotlane rm %s, to free the name",
		e.Name, tilde(e.Owner), e.Name)
}

// CheckOwner allows s to replace existing (the site registered under the same name) when it is
// the same project's, or existing was added by hand: dotlane up adopts it. A project's site
// cannot be taken by another project, or by dotlane add.
func CheckOwner(existing, s Site) error {
	if existing.Project != "" && existing.Project != s.Project {
		return &ConflictError{Name: s.Name, Owner: existing.Project}
	}
	return nil
}

func tilde(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

var (
	namePattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

// Validate checks the same rules as the Bash prototype, plus the DNS label length, and a PHP
// site's settings. Whether the folder and the PHP version exist is up to the daemon: a site
// stays registered when either goes away.
func (s Site) Validate() error {
	if !namePattern.MatchString(s.Name) || len(s.Name) > 63 {
		return &InvalidError{fmt.Sprintf("name must be lowercase letters, digits and dashes: %s", s.Name)}
	}
	if s.IsPHP() {
		return s.validatePHP()
	}
	if s.PHP != (PHP{}) {
		return &InvalidError{"a PHP site needs the folder it serves"}
	}
	if s.Port < 1 || s.Port > 65535 {
		return &InvalidError{fmt.Sprintf("not a port: %d", s.Port)}
	}
	return nil
}

func (s Site) validatePHP() error {
	p := s.PHP
	switch {
	case s.Port != 0:
		return &InvalidError{"a site has a port or a PHP folder, not both"}
	case !filepath.IsAbs(p.Root):
		return &InvalidError{fmt.Sprintf("not an absolute folder: %s", p.Root)}
	case p.Version != LatestPHP && !versionPattern.MatchString(p.Version):
		return &InvalidError{fmt.Sprintf("PHP version must be major.minor, such as 8.4, or %s: %q", LatestPHP, p.Version)}
	case p.Runtime != "" && p.Runtime != RuntimeFPM && p.Runtime != RuntimeFrankenPHP:
		return &InvalidError{fmt.Sprintf("PHP runtime must be %s or %s: %q", RuntimeFPM, RuntimeFrankenPHP, p.Runtime)}
	case p.Worker != "" && p.RuntimeOrDefault() != RuntimeFrankenPHP:
		return &InvalidError{"worker mode needs the FrankenPHP runtime"}
	case p.Worker != "" && (filepath.IsAbs(p.Worker) || !filepath.IsLocal(p.Worker)):
		return &InvalidError{fmt.Sprintf("the worker script must be inside the folder, relative to it: %s", p.Worker)}
	}
	return nil
}

// RuntimeOrDefault is the runtime p asks for, or the default one.
func (p PHP) RuntimeOrDefault() string {
	if p.Runtime == "" {
		return DefaultRuntime
	}
	return p.Runtime
}

// Registry is the set of sites, saved as JSON after every change.
type Registry struct {
	path  string
	mu    sync.Mutex
	sites map[string]Site
}

type document struct {
	Version int    `json:"version"`
	Sites   []Site `json:"sites"`
}

// Open loads the registry at path; a missing file is an empty registry.
func Open(path string) (*Registry, error) {
	r := &Registry{path: path, sites: map[string]Site{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, s := range doc.Sites {
		if err := s.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		r.sites[s.Name] = s
	}
	return r, nil
}

// List returns the sites sorted by name.
func (r *Registry) List() []Site {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listLocked()
}

func (r *Registry) listLocked() []Site {
	out := make([]Site, 0, len(r.sites))
	for _, s := range r.sites {
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b Site) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Get returns the site with that name, if any.
func (r *Registry) Get(name string) (Site, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sites[name]
	return s, ok
}

// Put adds or replaces a site and saves the registry.
func (r *Registry) Put(s Site) error {
	if err := s.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, had := r.sites[s.Name]
	r.sites[s.Name] = s
	if err := r.saveLocked(); err != nil {
		if had {
			r.sites[s.Name] = prev
		} else {
			delete(r.sites, s.Name)
		}
		return err
	}
	return nil
}

// Delete removes a site and saves the registry.
func (r *Registry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.sites[name]
	if !ok {
		return ErrNotFound
	}
	delete(r.sites, name)
	if err := r.saveLocked(); err != nil {
		r.sites[name] = prev
		return err
	}
	return nil
}

// saveLocked writes through a temporary file and a rename, so a crash never leaves half a file.
func (r *Registry) saveLocked() error {
	data, err := json.MarshalIndent(document{Version: 1, Sites: r.listLocked()}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(r.path), ".sites-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), r.path)
}
