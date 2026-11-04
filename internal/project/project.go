// Package project reads dotlane.toml, the sites a project declares, and works out what
// dotlane up and dotlane down change.
//
//	[sites.myapp]
//	port = 5173
//
//	[sites.shop]
//	root = "public"
//	php = "8.4"
package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/dotlane/dotlane/internal/sites"
)

// FileName is the project file, looked for in the current directory and its parents.
const FileName = "dotlane.toml"

// ErrNotFound means no dotlane.toml in the directory or any parent.
var ErrNotFound = errors.New("no " + FileName + " here or in a parent directory")

// Project is a dotlane.toml and the sites it declares.
type Project struct {
	Dir   string       // the file's directory, which identifies the project in the registry
	Path  string       // the file
	Sites []sites.Site // sorted by name, each with Project set to Dir
}

// file mirrors dotlane.toml. Unknown keys are rejected, so typos surface.
type file struct {
	Sites map[string]struct {
		Port int `toml:"port"`
		// A PHP site: the folder it serves, relative to the project, and the PHP version.
		Root string `toml:"root"`
		// A string, but read as anything so php = 8.4, a number, gets its own message.
		PHP     any    `toml:"php"`
		Runtime string `toml:"runtime"`
		Worker  string `toml:"worker"`
	} `toml:"sites"`
}

// Find returns the dotlane.toml in dir or its nearest parent that has one.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, FileName)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotFound
		}
		dir = parent
	}
}

// Load reads and validates the file at path.
func Load(path string) (*Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f file
	if err := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields().Decode(&f); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("%s: unknown setting\n%s", path, strict.String())
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Sites) == 0 {
		return nil, fmt.Errorf("%s lists no sites; add one:\n[sites.<name>]\nport = <port>\n\nor, for PHP files:\n[sites.<name>]\nroot = \"public\"\nphp = \"%s\"", path, sites.LatestPHP)
	}

	// The real path, so a project reached through a symlink is still the same project.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	p := &Project{Dir: dir, Path: filepath.Join(dir, FileName)}
	for name, s := range f.Sites {
		site := sites.Site{Name: name, Port: s.Port, Project: dir}
		version, ok := s.PHP.(string)
		if s.PHP != nil && !ok {
			return nil, fmt.Errorf("%s: [sites.%s]: php is a string: php = \"%v\"", path, name, s.PHP)
		}
		switch {
		case filepath.IsAbs(s.Root):
			return nil, fmt.Errorf("%s: [sites.%s]: root must be relative to the project: %s", path, name, s.Root)
		case s.PHP != nil:
			// No root means the project's own folder.
			site.PHP = sites.PHP{Root: filepath.Join(dir, s.Root), Version: version, Runtime: s.Runtime, Worker: s.Worker}
		case s.Root != "":
			return nil, fmt.Errorf("%s: [sites.%s]: root needs php: the PHP version, such as php = \"8.4\", or php = \"%s\"",
				path, name, sites.LatestPHP)
		case s.Runtime != "" || s.Worker != "":
			return nil, fmt.Errorf("%s: [sites.%s]: runtime and worker are for PHP sites, which need php = \"<version>\"", path, name)
		}
		if err := site.Validate(); err != nil {
			return nil, fmt.Errorf("%s: [sites.%s]: %w", path, name, err)
		}
		p.Sites = append(p.Sites, site)
	}
	slices.SortFunc(p.Sites, func(a, b sites.Site) int { return strings.Compare(a.Name, b.Name) })
	return p, nil
}

// Up compares the file with the registered sites. It returns the names other projects already
// use, and this project's registered sites the file no longer lists, which up removes.
func (p *Project) Up(registered []sites.Site) (conflicts []*sites.ConflictError, stale []string) {
	declared := map[string]sites.Site{}
	for _, s := range p.Sites {
		declared[s.Name] = s
	}
	for _, r := range registered {
		if s, ok := declared[r.Name]; ok {
			var c *sites.ConflictError
			if errors.As(sites.CheckOwner(r, s), &c) {
				conflicts = append(conflicts, c)
			}
		} else if r.Project == p.Dir {
			stale = append(stale, r.Name)
		}
	}
	return conflicts, stale
}

// Down returns the names of this project's registered sites, including any the file no longer
// lists.
func (p *Project) Down(registered []sites.Site) []string {
	var names []string
	for _, r := range registered {
		if r.Project == p.Dir {
			names = append(names, r.Name)
		}
	}
	return names
}
