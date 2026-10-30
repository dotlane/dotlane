// Package sites keeps the registry of sites: a name and the local port it forwards to.
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

// Site is https://<Name>.<tld> forwarded to localhost:<Port>.
type Site struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	// Project is the directory of the dotlane.toml that registered the site (dotlane up), or
	// empty for one added by hand (dotlane add).
	Project string `json:"project,omitempty"`
}

// ErrNotFound is returned when removing a site that does not exist.
var ErrNotFound = errors.New("no such site")

// InvalidError reports a site that breaks the naming or port rules.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

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
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Validate checks the same rules as the Bash prototype, plus the DNS label length.
func (s Site) Validate() error {
	if !namePattern.MatchString(s.Name) || len(s.Name) > 63 {
		return &InvalidError{fmt.Sprintf("name must be lowercase letters, digits and dashes: %s", s.Name)}
	}
	if s.Port < 1 || s.Port > 65535 {
		return &InvalidError{fmt.Sprintf("not a port: %d", s.Port)}
	}
	return nil
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
