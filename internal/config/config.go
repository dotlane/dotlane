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

	"github.com/pelletier/go-toml/v2"
)

// Config is the resolved configuration: defaults, overridden by the file.
type Config struct {
	TLDs    []string // domain endings, without the dot; sites answer on every one
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
	tldPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)
	servicePattern = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9-]+)+$`)
)

// Resolve picks the config file: flag (--config), else $DOTLANE_CONFIG, else
// ~/.config/dotlane/config.toml. The default file is optional, also when named explicitly (the
// login service always names it). Any other file must exist: a typo there would otherwise mean
// silently running with the defaults.
func Resolve(flag string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	def := filepath.Join(home, ".config", "dotlane", "config.toml")
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
	state, err := os.UserConfigDir() // macOS: ~/Library/Application Support
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		TLDs:      []string{"traix"},
		DNSPort:   10053,
		HTTPPort:  80,
		HTTPSPort: 443,
		StateDir:  filepath.Join(state, "dotlane"),

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
		cfg.TLDs = []string{raw.TLD}
	case len(raw.TLDs) > 0:
		cfg.TLDs = raw.TLDs
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
	for _, tld := range c.TLDs {
		if !tldPattern.MatchString(tld) {
			return fmt.Errorf("domain ending must be lowercase letters, digits and dashes: %q", tld)
		}
		if tld == "local" {
			return errors.New("domain ending cannot be local: macOS uses .local for Bonjour")
		}
		if seen[tld] {
			return fmt.Errorf("domain ending listed twice: %q", tld)
		}
		seen[tld] = true
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "\n# %s\ndns_port = %d\n", why, port); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SocketPath is where the daemon listens for the CLI.
func (c *Config) SocketPath() string { return filepath.Join(c.StateDir, "dotlane.sock") }
