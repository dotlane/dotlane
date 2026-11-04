package php

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dotlane/dotlane/internal/sites"
)

// Upstream is how Caddy reaches a PHP site.
type Upstream struct {
	Runtime    string // sites.RuntimeFPM (FastCGI) or sites.RuntimeFrankenPHP (HTTP)
	Name       string // the runtime, for pages and messages: "PHP-FPM 8.4", "FrankenPHP 1.13.4"
	PHPVersion string // the full PHP version, empty when Problem is set
	Socket     string // the Unix socket it listens on
	Log        string
	// Problem is why the site cannot be served, with the fix: no PHP for it, or no folder.
	Problem string
}

// Status is one runtime the Supervisor runs.
type Status struct {
	Name       string
	Runtime    string
	PHPVersion string
	Binary     string
	Running    bool
	Problem    string // why it is not running: how it last exited
	Log        string
	Sites      []string
}

// Supervisor runs the PHP runtimes the sites need: one PHP-FPM pool per PHP version, one
// FrankenPHP process per site. It restarts one that exits, waiting longer after each try.
type Supervisor struct {
	dir       string // <state>/php: configurations, sockets and logs
	httpsPort int
	find      func(context.Context) []Install

	mu    sync.Mutex
	procs map[string]*proc
}

// NewSupervisor returns a Supervisor keeping its files in stateDir/php.
func NewSupervisor(stateDir string, httpsPort int) *Supervisor {
	return &Supervisor{
		dir:       filepath.Join(stateDir, "php"),
		httpsPort: httpsPort,
		find:      func(ctx context.Context) []Install { return Find(ctx, stateDir) },
		procs:     map[string]*proc{},
	}
}

// spec is a process the sites need.
type spec struct {
	key     string // fpm-8.4, frankenphp-<site>
	name    string
	install Install
	config  string // the configuration file's content
	file    string // where it goes
	args    []string
	socket  string
	log     string
	sites   []string
}

func (s spec) same(o spec) bool {
	return s.install == o.install && s.config == o.config && slices.Equal(s.args, o.args)
}

// Check says whether a site can be served, without starting anything: its folder (and worker
// script) exist, and an installed runtime fits it. The daemon refuses a new site that fails.
func (s *Supervisor) Check(ctx context.Context, site sites.Site) error {
	if err := checkFiles(site.PHP); err != nil {
		return err
	}
	_, err := Choose(s.find(ctx), site.PHP)
	return err
}

func checkFiles(p sites.PHP) error {
	if info, err := os.Stat(p.Root); err != nil || !info.IsDir() {
		return fmt.Errorf("no folder at %s", p.Root)
	}
	if p.Worker != "" {
		if _, err := os.Stat(filepath.Join(p.Root, p.Worker)); err != nil {
			return fmt.Errorf("no worker script at %s", filepath.Join(p.Root, p.Worker))
		}
	}
	return nil
}

// Sync starts the runtimes the PHP sites in list need, restarts those whose configuration
// changed and stops the rest. It waits a few seconds for new ones to listen, so a page opened
// right after works. It returns how to reach each PHP site, by name.
func (s *Supervisor) Sync(ctx context.Context, list []sites.Site) map[string]Upstream {
	installs := s.find(ctx)
	ups := map[string]Upstream{}
	want := map[string]spec{}
	for _, site := range list {
		if !site.IsPHP() {
			continue
		}
		in, err := Choose(installs, site.PHP)
		if err == nil {
			err = checkFiles(site.PHP)
		}
		if err != nil {
			ups[site.Name] = Upstream{Runtime: site.PHP.RuntimeOrDefault(), Name: runtimeName(site.PHP), Problem: err.Error()}
			continue
		}
		sp := s.spec(site, in)
		if prev, ok := want[sp.key]; ok { // another site on the same PHP-FPM pool
			sp = prev
		}
		sp.sites = append(sp.sites, site.Name)
		want[sp.key] = sp
		ups[site.Name] = Upstream{Runtime: in.Runtime, Name: sp.name, PHPVersion: in.PHPVersion, Socket: sp.socket, Log: sp.log}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for key, p := range s.procs {
		if w, ok := want[key]; !ok || !w.same(p.spec) {
			p.stop()
			delete(s.procs, key)
		}
	}
	var started []*proc
	for key, w := range want {
		if p, ok := s.procs[key]; ok {
			p.setSites(w.sites)
			continue
		}
		p := startProc(w)
		s.procs[key] = p
		started = append(started, p)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, p := range started {
		for !p.listening() && time.Now().Before(deadline) && ctx.Err() == nil && !p.exited() {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return ups
}

func runtimeName(p sites.PHP) string {
	if p.RuntimeOrDefault() == sites.RuntimeFrankenPHP {
		return "FrankenPHP"
	}
	if p.Version == sites.LatestPHP {
		return "PHP-FPM"
	}
	return "PHP-FPM " + p.Version
}

func (s *Supervisor) spec(site sites.Site, in Install) spec {
	if in.Runtime == sites.RuntimeFPM {
		key := "fpm-" + in.Minor()
		sp := spec{key: key, name: "PHP-FPM " + in.Minor(), install: in,
			file: filepath.Join(s.dir, key+".conf"), socket: s.socket(key), log: filepath.Join(s.dir, key+".log")}
		sp.config = fpmConfig(in.Minor(), sp.socket, sp.log)
		sp.args = []string{"--nodaemonize", "--fpm-config", sp.file}
		return sp
	}
	key := "frankenphp-" + site.Name
	sp := spec{key: key, name: "FrankenPHP " + in.Version, install: in,
		file: filepath.Join(s.dir, key+".Caddyfile"), socket: s.socket(key), log: filepath.Join(s.dir, key+".log")}
	sp.config = frankenPHPConfig(site, sp.socket, filepath.Join(s.dir, "frankenphp-data"), s.httpsPort)
	sp.args = []string{"run", "--config", sp.file, "--adapter", "caddyfile"}
	return sp
}

// maxSocket is the longest Unix socket path every OS takes (macOS: 104 bytes with the NUL).
const maxSocket = 103

// socket is where the runtime keyed key listens: <state>/php/<key>.sock, or a shorter name
// when a long site name would make the path too long for a socket.
func (s *Supervisor) socket(key string) string {
	path := filepath.Join(s.dir, key+".sock")
	if len(path) > maxSocket {
		path = filepath.Join(s.dir, fmt.Sprintf("%x", sha256.Sum256([]byte(key)))[:12]+".sock")
	}
	return path
}

// Status lists the runtimes running or being restarted, by name.
func (s *Supervisor) Status() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.procs))
	for _, p := range s.procs {
		out = append(out, p.status())
	}
	slices.SortFunc(out, func(a, b Status) int { return strings.Compare(a.Name+a.Log, b.Name+b.Log) })
	return out
}

// Stop stops every runtime.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, p := range s.procs {
		p.stop()
		delete(s.procs, key)
	}
}

// proc keeps one runtime running.
type proc struct {
	spec spec
	quit chan struct{}
	done chan struct{}

	mu      sync.Mutex
	sites   []string
	problem string // how it last exited, or why it could not start
	out     bool   // it is not running now
}

func startProc(sp spec) *proc {
	p := &proc{spec: sp, sites: sp.sites, quit: make(chan struct{}), done: make(chan struct{})}
	go p.run()
	return p
}

// run starts the process and starts it again when it exits, after 1 s, then 2, 4… up to 30 s;
// a run that lasted a minute starts the wait over.
func (p *proc) run() {
	defer close(p.done)
	wait := time.Second
	for {
		began := time.Now()
		err := p.once()
		if err == nil { // stopped
			return
		}
		p.mu.Lock()
		p.problem, p.out = err.Error(), true
		p.mu.Unlock()
		if time.Since(began) > time.Minute {
			wait = time.Second
		}
		select {
		case <-p.quit:
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
}

// once runs the process until it exits (an error saying how) or is stopped (nil).
func (p *proc) once() error {
	sp := p.spec
	if err := os.MkdirAll(filepath.Dir(sp.file), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(sp.file, []byte(sp.config), 0o600); err != nil {
		return err
	}
	os.Remove(sp.socket) // left by a run that crashed; PHP-FPM will not bind over it
	log, err := openLog(sp.log)
	if err != nil {
		return err
	}
	defer log.Close()
	fmt.Fprintf(log, "--- %s Dotlane starts %s: %s %s\n", time.Now().Format(time.RFC3339), sp.name, sp.install.Binary, strings.Join(sp.args, " "))

	cmd := exec.Command(sp.install.Binary, sp.args...)
	cmd.Dir = filepath.Dir(sp.file)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start %s: %w", sp.install.Binary, err)
	}
	began := time.Now()
	p.mu.Lock()
	p.problem, p.out = "", false
	p.mu.Unlock()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-p.quit:
		terminate(cmd.Process)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-exited
		}
		os.Remove(sp.socket)
		return nil
	case err := <-exited:
		if err == nil {
			err = errors.New("exit status 0")
		}
		msg := fmt.Sprintf("%s exited (%v)", sp.name, err)
		// One that stops right away usually says why last, such as a configuration error.
		if last := lastLine(sp.log); last != "" && time.Since(began) < 10*time.Second {
			msg += ": " + last
		}
		return errors.New(msg)
	}
}

func (p *proc) stop() {
	close(p.quit)
	<-p.done
}

func (p *proc) exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out
}

func (p *proc) setSites(names []string) {
	p.mu.Lock()
	p.sites = names
	p.mu.Unlock()
}

// listening reports whether the runtime accepts connections on its socket.
func (p *proc) listening() bool {
	conn, err := net.DialTimeout("unix", p.spec.socket, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (p *proc) status() Status {
	running := p.listening()
	p.mu.Lock()
	defer p.mu.Unlock()
	st := Status{Name: p.spec.name, Runtime: p.spec.install.Runtime, PHPVersion: p.spec.install.PHPVersion,
		Binary: p.spec.install.Binary, Running: running, Log: p.spec.log, Sites: slices.Clone(p.sites)}
	if !running {
		st.Problem = p.problem
		if st.Problem == "" {
			st.Problem = "starting, or not listening on " + p.spec.socket
		}
	}
	return st
}

// maxLog is the size above which a runtime's log starts over when the runtime starts.
const maxLog = 5 << 20

func openLog(path string) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > maxLog {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}

// lastLine is the log's last non-empty line, which usually says why a runtime stopped.
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, 4096)
	if info, err := f.Stat(); err == nil && info.Size() > int64(len(buf)) {
		f.Seek(info.Size()-int64(len(buf)), io.SeekStart)
	}
	n, _ := f.Read(buf)
	lines := bytes.Split(bytes.TrimSpace(buf[:n]), []byte("\n"))
	return strings.TrimSpace(string(lines[len(lines)-1]))
}
