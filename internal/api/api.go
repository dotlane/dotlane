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

	"github.com/pedrodee/dotlane/internal/sites"
)

// Site is a site as the daemon reports it.
type Site struct {
	Name string   `json:"name"`
	Port int      `json:"port"`
	URLs []string `json:"urls"`
	// Ready is false when the certificate was not served within the wait after adding;
	// the site usually works a few seconds later.
	Ready bool `json:"ready,omitempty"`
}

// Status describes the running daemon.
type Status struct {
	Version   string   `json:"version"`
	PID       int      `json:"pid"`
	TLDs      []string `json:"tlds"`
	DNSAddr   string   `json:"dns_addr"`
	HTTPSPort int      `json:"https_port"`
	StateDir  string   `json:"state_dir"`
}

// Backend is what the daemon provides to the handler.
type Backend interface {
	Status() Status
	Sites() []Site
	AddSite(ctx context.Context, s sites.Site) (Site, error)
	RemoveSite(name string) error
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
			Port int `json:"port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorBody{"invalid request body: " + err.Error()})
			return
		}
		site, err := b.AddSite(r.Context(), sites.Site{Name: r.PathValue("name"), Port: body.Port})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, site)
	})
	mux.HandleFunc("DELETE /v1/sites/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := b.RemoveSite(r.PathValue("name")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func writeError(w http.ResponseWriter, err error) {
	var invalid *sites.InvalidError
	switch {
	case errors.As(err, &invalid):
		writeJSON(w, http.StatusBadRequest, errorBody{err.Error()})
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

// AddSite adds or updates a site and waits until its certificate is served.
func (c *Client) AddSite(ctx context.Context, name string, port int) (Site, error) {
	var s Site
	return s, c.do(ctx, http.MethodPut, "/v1/sites/"+url.PathEscape(name), map[string]int{"port": port}, &s)
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
