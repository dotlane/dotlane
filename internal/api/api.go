// Package api is the control interface between the CLI (and later the desktop app) and the
// daemon: JSON over HTTP on a Unix socket only the user can open.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/dotlane/dotlane/internal/sites"
)

// Version is the version of this API. It goes up only with a change an older client or daemon
// cannot handle, so the CLI can say which side to update instead of failing on a request.
const Version = 1

// Site is a site as the daemon reports it.
type Site struct {
	Name string   `json:"name"`
	Port int      `json:"port,omitempty"` // 0 for a PHP site
	URLs []string `json:"urls"`           // one per domain ending, the primary's first
	// Project is the directory of the dotlane.toml that registered the site; empty when added
	// by hand.
	Project string `json:"project,omitempty"`
	// Ready is false when the certificate was not served within the wait after adding;
	// the site usually works a few seconds later.
	Ready bool `json:"ready,omitempty"`
	// PHP is set for a PHP site.
	PHP *PHPSite `json:"php,omitempty"`
}

// PHPSite is how a PHP site is served. Its Runtime is always set: the one the site asks for, or
// the default.
type PHPSite struct {
	sites.PHP
	// PHPVersion is the full version of the PHP that serves the site ("8.4.13"), empty when
	// none can; Problem then says why, with the fix.
	PHPVersion string `json:"php_version,omitempty"`
	Problem    string `json:"problem,omitempty"`
	Log        string `json:"log,omitempty"` // the runtime's log file
}

// Runtime is a PHP runtime the daemon runs: a PHP-FPM pool, shared by the sites on one PHP
// version, or a site's FrankenPHP process.
type Runtime struct {
	Name       string   `json:"name"`    // "PHP-FPM 8.4", "FrankenPHP (shop)"
	Runtime    string   `json:"runtime"` // sites.RuntimeFPM or sites.RuntimeFrankenPHP
	PHPVersion string   `json:"php_version"`
	Binary     string   `json:"binary"`
	Running    bool     `json:"running"`
	Problem    string   `json:"problem,omitempty"` // why it is not running, with the fix
	Log        string   `json:"log"`
	Sites      []string `json:"sites"`
}

// Status describes the running daemon.
type Status struct {
	Version    string   `json:"version"`
	PID        int      `json:"pid"`
	TLDs       []string `json:"tlds"`
	PrimaryTLD string   `json:"primary_tld"` // the first of TLDs, shown first
	DNSAddr    string   `json:"dns_addr"`
	HTTPSPort  int      `json:"https_port"`
	StateDir   string   `json:"state_dir"`
	// Started is when the daemon started; a binary changed after that is not what it runs.
	Started time.Time `json:"started"`
	// API is the daemon's Version; see APIVersion.
	API int `json:"api,omitempty"`
	// Features are what this daemon can do beyond its API version, for a client newer than it:
	// FeaturePHP for PHP sites.
	Features []string `json:"features,omitempty"`
	// PHP are the PHP runtimes the daemon runs for its sites.
	PHP []Runtime `json:"php,omitempty"`
}

// FeaturePHP means the daemon serves PHP sites.
const FeaturePHP = "php"

// APIVersion is the version of the API the daemon serves: 1 for a daemon from before it was
// reported.
func (s Status) APIVersion() int {
	if s.API == 0 {
		return 1
	}
	return s.API
}

// Backend is what the daemon provides to the handler.
type Backend interface {
	Status() Status
	Sites() []Site
	AddSite(ctx context.Context, s sites.Site) (Site, error)
	RemoveSite(ctx context.Context, name string) error
}

type errorBody struct {
	Error string `json:"error"`
}

// Handler serves the API for b.
func Handler(b Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("GET /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Sites())
	})
	mux.HandleFunc("PUT /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Port    int        `json:"port"`
			Project string     `json:"project"`
			PHP     *sites.PHP `json:"php"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{"invalid request body: " + err.Error()})
			return
		}
		s := sites.Site{Name: r.PathValue("name"), Port: body.Port, Project: body.Project}
		if body.PHP != nil {
			s.PHP = *body.PHP
		}
		site, err := b.AddSite(r.Context(), s)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, site)
	})
	mux.HandleFunc("DELETE /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := b.RemoveSite(r.Context(), r.PathValue("name")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func writeError(w http.ResponseWriter, err error) {
	var invalid *sites.InvalidError
	var conflict *sites.ConflictError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, errorBody{err.Error()})
	case errors.Is(err, sites.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errorBody{err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, errorBody{err.Error()})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// ErrNotRunning means nothing answers on the socket.
var ErrNotRunning = errors.New("the dotlane daemon is not running")

// Client talks to the daemon's socket.
type Client struct {
	http *http.Client
}

// NewClient returns a client for the daemon listening on socketPath.
func NewClient(socketPath string) *Client {
	return &Client{http: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
	}}
}

// Status returns the daemon's status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var s Status
	return s, c.do(ctx, http.MethodGet, "/v1/status", nil, &s)
}

// Sites lists the registered sites.
func (c *Client) Sites(ctx context.Context) ([]Site, error) {
	var out []Site
	return out, c.do(ctx, http.MethodGet, "/v1/sites", nil, &out)
}

// AddSite adds or updates a site and waits until its certificate is served. Its Project is the
// directory of the dotlane.toml it comes from, or empty for a site added by hand.
func (c *Client) AddSite(ctx context.Context, site sites.Site) (Site, error) {
	var s Site
	body := map[string]any{"port": site.Port, "project": site.Project}
	if site.IsPHP() {
		body["php"] = site.PHP
	}
	return s, c.do(ctx, http.MethodPut, "/v1/sites/"+url.PathEscape(site.Name), body, &s)
}

// RemoveSite removes a site.
func (c *Client) RemoveSite(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/sites/"+url.PathEscape(name), nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://dotlane"+path, body)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return ErrNotRunning
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e errorBody
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("daemon answered %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
