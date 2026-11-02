// Package config reads Dotlane's machine settings from config.toml.
//
// The file is optional; every setting has a default. It is the same file the Bash prototype
// reads (tld, dns_port), so both can share it while the Go version takes over.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/dotlane/dotlane/internal/tld"
)

// DefaultTLD is the domain ending when the file sets none. Until v0.2.0 it was traix; the
// integration tests build with -X to set it back, to test the upgrade from it.
var DefaultTLD = "dotlane"

// Config is the resolved configuration: defaults, overridden by the file.
type Config struct {
	TLDs    []string // domain endings, without the dot; sites answer on every one
	TLDsSet bool     // the file chose the endings (tld or tlds), not DefaultTLD
	DNSPort int      // DNS responder, on 127.0.0.1
	// DNSPortSet is true when the file chose the DNS port. Otherwise install may pick another
	// free one when the default is taken, and save it with SetDNSPort.
	DNSPortSet bool
	HTTPPort   int // redirects to HTTPS
	HTTPSPort  int
	StateDir   string // sites, certificate authority, control socket
	// ServiceName identifies the login service (launchd label) and its log, so a development
	// install can sit next to a real one.
	ServiceName string
	Path        string // file the settings came from; empty when only defaults apply
	// Warnings are about endings the file sets that dotlane tld add would refuse or warn
	// about. They are not errors, so an ending chosen before those rules keeps working.
	Warnings []string
}

// file mirrors config.toml. Unknown keys are rejected, so typos surface.
type file struct {
	TLD       string   `toml:"tld"`
	TLDs      []string `toml:"tlds"`
	DNSPort   int      `toml:"dns_port"`
	HTTPPort  int      `toml:"http_port"`
	HTTPSPort int      `toml:"https_port"`
	StateDir  string   `toml:"state_dir"`
	Service   string   `toml:"service_name"`
}

var (
	servicePattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)+$`)
	tldSetting     = regexp.MustCompile(`^\s*tlds?\s*=`)
)

// Resolve picks the config file: flag (--config), else $DOTLANE_CONFIG, else DefaultPath. The
// default file is optional, also when named explicitly (the
// login service always names it). Any other file must exist: a typo there would otherwise mean
// silently running with the defaults.
func Resolve(flag string) (string, error) {
	def, err := DefaultPath()
	if err != nil {
		return "", err
	}
	path := flag
	if path == "" {
		path = os.Getenv("DOTLANE_CONFIG")
	}
	if path == "" || path == def {
		return def, nil
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("config file %s: %w", path, errors.Unwrap(err))
	}
	return path, nil
}

// Load reads path (a missing file means defaults) and validates the result.
func Load(path string) (*Config, error) {
	state, err := defaultStateDir()
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		TLDs:      []string{DefaultTLD},
		DNSPort:   DefaultDNSPort,
		HTTPPort:  80,
		HTTPSPort: 443,
		StateDir:  state,

		ServiceName: "dev.dotlane",
	}

	// Absolute, so a relative state_dir stays tied to the file wherever the command runs.
	if path, err = filepath.Abs(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var raw file
	if err := toml.NewDecoder(f).DisallowUnknownFields().Decode(&raw); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return nil, fmt.Errorf("%s: unknown setting\n%s", path, strict.String())
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path

	switch {
	case raw.TLD != "" && len(raw.TLDs) > 0:
		return nil, fmt.Errorf("%s: set tld or tlds, not both", path)
	case raw.TLD != "":
		cfg.TLDs, cfg.TLDsSet = []string{raw.TLD}, true
	case len(raw.TLDs) > 0:
		cfg.TLDs, cfg.TLDsSet = raw.TLDs, true
	}
	if raw.DNSPort != 0 {
		cfg.DNSPort, cfg.DNSPortSet = raw.DNSPort, true
	}
	if raw.HTTPPort != 0 {
		cfg.HTTPPort = raw.HTTPPort
	}
	if raw.HTTPSPort != 0 {
		cfg.HTTPSPort = raw.HTTPSPort
	}
	if raw.Service != "" {
		cfg.ServiceName = raw.Service
	}
	if raw.StateDir != "" {
		cfg.StateDir = raw.StateDir
		if !filepath.IsAbs(cfg.StateDir) {
			cfg.StateDir = filepath.Join(filepath.Dir(path), cfg.StateDir)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	seen := map[string]bool{}
	for _, t := range c.TLDs {
		if err := tld.Syntax(t); err != nil {
			return err
		}
		if t == "local" {
			return errors.New("domain ending cannot be local: it belongs to multicast DNS (Bonjour, Avahi)")
		}
		if seen[t] {
			return fmt.Errorf("domain ending listed twice: %q", t)
		}
		seen[t] = true
		if warning, err := tld.Check(t); err != nil {
			c.Warnings = append(c.Warnings, err.Error())
		} else if warning != "" {
			c.Warnings = append(c.Warnings, warning)
		}
	}
	ports := map[string]int{"dns_port": c.DNSPort, "http_port": c.HTTPPort, "https_port": c.HTTPSPort}
	for key, port := range ports {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s is not a port: %d", key, port)
		}
	}
	if !servicePattern.MatchString(c.ServiceName) {
		return fmt.Errorf("service_name must be a reverse-DNS name such as dev.dotlane: %q", c.ServiceName)
	}
	if c.HTTPPort == c.HTTPSPort {
		return errors.New("http_port and https_port must differ")
	}
	return nil
}

// SetDNSPort records a DNS port in the config file at path, creating it if needed, with a
// comment saying why. Only for files that do not set dns_port yet (see DNSPortSet).
func SetDNSPort(path string, port int, why string) error {
	return appendSetting(path, why, fmt.Sprintf("dns_port = %d", port))
}

// PinTLDs records endings in the config file at path, creating it if needed, with a comment
// saying why. Only for files that do not set them yet (see TLDsSet); SetTLDs changes them.
func PinTLDs(path string, tlds []string, why string) error {
	quoted := make([]string, len(tlds))
	for i, t := range tlds {
		if err := tld.Syntax(t); err != nil {
			return err
		}
		quoted[i] = strconv.Quote(t)
	}
	return appendSetting(path, why, "tlds = ["+strings.Join(quoted, ", ")+"]")
}

// appendSetting adds a commented setting at the end of the config file at path.
func appendSetting(path, why, setting string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "\n# %s\n%s\n", why, setting); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SetTLDs records the domain endings in the config file at path, creating it if needed; the
// first is the primary. It replaces the tld or tlds setting where it stands, as one tlds line,
// and keeps the rest of the file, its comments and a symlink to it (dotfiles) as they are.
func SetTLDs(path string, tlds []string) error {
	if len(tlds) == 0 {
		return errors.New("at least one domain ending is needed")
	}
	seen := map[string]bool{}
	quoted := make([]string, len(tlds))
	for i, t := range tlds {
		if err := tld.Syntax(t); err != nil {
			return err
		}
		if seen[t] {
			return fmt.Errorf("domain ending listed twice: %q", t)
		}
		seen[t] = true
		quoted[i] = strconv.Quote(t)
	}
	setting := "tlds = [" + strings.Join(quoted, ", ") + "]"

	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	have, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return replaceFile(path, []byte(withSetting(string(have), setting)))
}

// withSetting replaces every tld or tlds setting in body with setting, at the first one's place,
// keeping its indent and trailing comment; with none, setting is appended.
func withSetting(body, setting string) string {
	lines := strings.SplitAfter(body, "\n")
	var out strings.Builder
	done := false
	for i := 0; i < len(lines); i++ {
		loc := tldSetting.FindStringIndex(lines[i])
		if loc == nil {
			out.WriteString(lines[i])
			continue
		}
		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		end, comment := valueEnd(lines, i, loc[1])
		if !done {
			out.WriteString(indent + setting)
			if comment != "" {
				out.WriteString("   " + comment)
			}
			out.WriteString("\n")
			done = true
		}
		i = end
	}
	if !done {
		if body != "" && !strings.HasSuffix(body, "\n") {
			out.WriteString("\n")
		}
		out.WriteString(setting + "\n")
	}
	return out.String()
}

// valueEnd finds where the value starting at lines[i][pos:] ends: the line holding its last
// character (an array may span lines), and the comment after it on that line, if any.
func valueEnd(lines []string, i, pos int) (int, string) {
	depth, began := 0, false
	for ; i < len(lines); i, pos = i+1, 0 {
		l := strings.TrimRight(lines[i], "\r\n")
		var quote byte
		for j := pos; j < len(l); j++ {
			switch c := l[j]; {
			case quote != 0:
				if c == '\\' && quote == '"' {
					j++
				} else if c == quote {
					quote = 0
				}
			case c == '"' || c == '\'':
				quote, began = c, true
			case c == '#':
				if began && depth == 0 {
					return i, strings.TrimSpace(l[j:])
				}
				j = len(l) // a comment inside the array
			case c == '[':
				depth, began = depth+1, true
			case c == ']':
				depth--
			case c != ' ' && c != '\t':
				began = true
			}
		}
		if began && depth == 0 {
			return i, ""
		}
	}
	return len(lines) - 1, ""
}

// replaceFile writes data to path through a file next to it, so a reader never sees half a
// file, and keeps the file's mode.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // after a failure; gone after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Primary is the first domain ending: commands print its URL first.
func (c *Config) Primary() string { return c.TLDs[0] }

// SocketPath is where the daemon listens for the CLI.
func (c *Config) SocketPath() string { return filepath.Join(c.StateDir, "dotlane.sock") }
