package proxy

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() { caddy.RegisterModule(PHPFiles{}) }

// PHPFiles serves a PHP site from its folder with PHP-FPM, as Caddy's php_fastcgi and
// file_server directives would: a .php file runs, a folder with an index.php gets its trailing
// slash and runs it, another file is sent as it is, and anything else runs the front controller,
// index.php. Hidden files (.env, .git) are never sent.
//
// It is Dotlane's own because Caddy's FastCGI module brings its file server, and with it the
// template, Markdown and syntax-highlighting libraries: 5 MB more in the binary.
type PHPFiles struct {
	Root   string `json:"root"`   // the site's folder
	Socket string `json:"socket"` // PHP-FPM's Unix socket

	logger *zap.Logger
}

// CaddyModule implements caddy.Module.
func (PHPFiles) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.handlers.dotlane_php", New: func() caddy.Module { return new(PHPFiles) }}
}

// Provision implements caddy.Provisioner.
func (h *PHPFiles) Provision(ctx caddy.Context) error {
	h.logger = ctx.Logger()
	return nil
}

var (
	_ caddyhttp.MiddlewareHandler = (*PHPFiles)(nil)
	_ caddy.Provisioner           = (*PHPFiles)(nil)
)

// ServeHTTP implements caddyhttp.MiddlewareHandler.
func (h *PHPFiles) ServeHTTP(w http.ResponseWriter, r *http.Request, _ caddyhttp.Handler) error {
	upath := path.Clean("/" + r.URL.Path)
	for _, part := range strings.Split(upath, "/") {
		if strings.HasPrefix(part, ".") && part != ".well-known" {
			return caddyhttp.Error(http.StatusNotFound, nil)
		}
	}
	if script, info, ok := splitPHP(upath); ok && h.isFile(script) {
		return h.php(w, r, script, info)
	}
	file := h.file(upath)
	switch fi, err := os.Stat(file); {
	case err == nil && fi.IsDir():
		index := path.Join(upath, "index.php")
		if h.isFile(index) {
			if !strings.HasSuffix(r.URL.Path, "/") {
				http.Redirect(w, r, r.URL.EscapedPath()+"/"+prefixed(r.URL.RawQuery), http.StatusPermanentRedirect)
				return nil
			}
			return h.php(w, r, index, "")
		}
	case err == nil && fi.Mode().IsRegular() && !strings.HasSuffix(upath, ".php"):
		f, err := os.Open(file)
		if err != nil {
			return caddyhttp.Error(http.StatusForbidden, err)
		}
		defer f.Close()
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
		return nil
	}
	return h.php(w, r, "/index.php", "")
}

func prefixed(query string) string {
	if query == "" {
		return ""
	}
	return "?" + query
}

// splitPHP splits a path at its first ".php" segment: /index.php/users is /index.php with
// PATH_INFO /users.
func splitPHP(upath string) (script, pathInfo string, ok bool) {
	for i := 0; ; {
		j := strings.Index(upath[i:], ".php")
		if j < 0 {
			return "", "", false
		}
		end := i + j + len(".php")
		if end == len(upath) || upath[end] == '/' {
			return upath[:end], upath[end:], true
		}
		i = end
	}
}

func (h *PHPFiles) file(upath string) string { return filepath.Join(h.Root, filepath.FromSlash(upath)) }

func (h *PHPFiles) isFile(upath string) bool {
	fi, err := os.Stat(h.file(upath))
	return err == nil && fi.Mode().IsRegular()
}

// php runs script (a path under Root) through PHP-FPM and writes its response. A PHP-FPM that
// does not answer is a 502, which gets Dotlane's page for it.
func (h *PHPFiles) php(w http.ResponseWriter, r *http.Request, script, pathInfo string) error {
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(r.Context(), "unix", h.Socket)
	if err != nil {
		return caddyhttp.Error(http.StatusBadGateway, err)
	}
	defer conn.Close()
	stop := context.AfterFunc(r.Context(), func() { conn.Close() }) // the browser went away
	defer stop()

	bw := bufio.NewWriterSize(conn, 64<<10)
	if err := writeRequest(bw, h.env(r, script, pathInfo), r.Body); err != nil {
		return caddyhttp.Error(http.StatusBadGateway, err)
	}
	if err := bw.Flush(); err != nil {
		return caddyhttp.Error(http.StatusBadGateway, err)
	}

	stdout, stderr := readResponse(conn)
	defer stdout.Close()
	tp := textproto.NewReader(bufio.NewReader(stdout))
	header, err := tp.ReadMIMEHeader()
	if err != nil && !(errors.Is(err, io.EOF) && len(header) > 0) {
		return caddyhttp.Error(http.StatusBadGateway, fmt.Errorf("PHP-FPM: %w%s", err, logged(stderr)))
	}
	status := http.StatusOK
	if s := header.Get("Status"); s != "" {
		if code, err := strconv.Atoi(strings.Fields(s)[0]); err == nil {
			status = code
		}
		header.Del("Status")
	} else if header.Get("Location") != "" {
		status = http.StatusFound
	}
	for k, v := range header {
		w.Header()[k] = v
	}
	w.WriteHeader(status)
	_, err = io.Copy(flushWriter{w}, tp.R)
	if msg := logged(stderr); msg != "" {
		h.logger.Warn("PHP", zap.String("script", script), zap.String("stderr", strings.TrimPrefix(msg, ": ")))
	}
	if err != nil && r.Context().Err() == nil {
		h.logger.Warn("PHP-FPM response cut short", zap.Error(err))
	}
	return nil
}

// stderrBuffer collects what PHP-FPM sends on stderr: PHP's warnings and errors.
type stderrBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *stderrBuffer) write(p []byte) {
	s.mu.Lock()
	s.b.Write(p)
	s.mu.Unlock()
}

func logged(stderr *stderrBuffer) string {
	stderr.mu.Lock()
	defer stderr.mu.Unlock()
	if stderr.b.Len() == 0 {
		return ""
	}
	return ": " + strings.TrimSpace(stderr.b.String())
}

// flushWriter sends what PHP writes as it comes (server-sent events, flush()).
type flushWriter struct{ w http.ResponseWriter }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	http.NewResponseController(f.w).Flush()
	return n, err
}

// env is the CGI environment PHP sees: what Caddy's FastCGI transport sends, over HTTPS.
func (h *PHPFiles) env(r *http.Request, script, pathInfo string) map[string]string {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, "443"
	}
	remoteIP, remotePort, _ := net.SplitHostPort(r.RemoteAddr)
	env := map[string]string{
		"GATEWAY_INTERFACE": "CGI/1.1",
		"SERVER_SOFTWARE":   "Dotlane",
		"SERVER_PROTOCOL":   r.Proto,
		"SERVER_NAME":       host,
		"SERVER_PORT":       port,
		"HTTPS":             "on",
		"REQUEST_SCHEME":    "https",
		"REQUEST_METHOD":    r.Method,
		"REQUEST_URI":       r.URL.RequestURI(), // nothing rewrote it: the browser's path and query
		"QUERY_STRING":      r.URL.RawQuery,
		"REMOTE_ADDR":       remoteIP,
		"REMOTE_HOST":       remoteIP,
		"REMOTE_PORT":       remotePort,
		"DOCUMENT_ROOT":     h.Root,
		"DOCUMENT_URI":      script + pathInfo,
		"SCRIPT_NAME":       script,
		"SCRIPT_FILENAME":   h.file(script),
		"PATH_INFO":         pathInfo,
		"CONTENT_TYPE":      r.Header.Get("Content-Type"),
		"CONTENT_LENGTH":    "",
	}
	if r.ContentLength > 0 {
		env["CONTENT_LENGTH"] = strconv.FormatInt(r.ContentLength, 10)
	}
	if pathInfo != "" {
		env["PATH_TRANSLATED"] = h.file(pathInfo)
	}
	for k, v := range r.Header {
		if k == "Proxy" { // httpoxy: PHP would read it as HTTP_PROXY
			continue
		}
		env["HTTP_"+strings.ToUpper(strings.ReplaceAll(k, "-", "_"))] = strings.Join(v, ", ")
	}
	env["HTTP_HOST"] = r.Host
	return env
}

// FastCGI records (https://fastcgi-archives.github.io/FastCGI_Specification.html).
const (
	fcgiBeginRequest = 1
	fcgiEndRequest   = 3
	fcgiParams       = 4
	fcgiStdin        = 5
	fcgiStdout       = 6
	fcgiStderr       = 7
	fcgiResponder    = 1
	fcgiMaxContent   = 65535
)

// writeRequest sends one request: its parameters, then the body.
func writeRequest(w io.Writer, env map[string]string, body io.Reader) error {
	if err := writeRecord(w, fcgiBeginRequest, []byte{0, fcgiResponder, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var params []byte
	for k, v := range env {
		params = appendLength(params, len(k))
		params = appendLength(params, len(v))
		params = append(append(params, k...), v...)
	}
	if err := writeStream(w, fcgiParams, params); err != nil {
		return err
	}
	buf := make([]byte, fcgiMaxContent)
	for body != nil {
		n, err := body.Read(buf)
		if n > 0 {
			if werr := writeRecord(w, fcgiStdin, buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}
	return writeRecord(w, fcgiStdin, nil)
}

func appendLength(b []byte, n int) []byte {
	if n < 128 {
		return append(b, byte(n))
	}
	return binary.BigEndian.AppendUint32(b, uint32(n)|1<<31)
}

// writeStream sends content as records of type typ, then the empty record that ends it.
func writeStream(w io.Writer, typ byte, content []byte) error {
	for len(content) > 0 {
		n := min(len(content), fcgiMaxContent)
		if err := writeRecord(w, typ, content[:n]); err != nil {
			return err
		}
		content = content[n:]
	}
	return writeRecord(w, typ, nil)
}

// writeRecord sends one record of request 1 (one request per connection), padded to 8 bytes.
func writeRecord(w io.Writer, typ byte, content []byte) error {
	pad := -len(content) & 7
	header := []byte{1, typ, 0, 1, byte(len(content) >> 8), byte(len(content)), byte(pad), 0}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(content); err != nil {
		return err
	}
	_, err := w.Write(make([]byte, pad))
	return err
}

// readResponse reads PHP-FPM's records in the background: stdout as a stream, stderr into a
// buffer that is complete once stdout ends.
func readResponse(conn net.Conn) (io.ReadCloser, *stderrBuffer) {
	pr, pw := io.Pipe()
	stderr := new(stderrBuffer)
	go func() {
		br := bufio.NewReader(conn)
		header := make([]byte, 8)
		for {
			if _, err := io.ReadFull(br, header); err != nil {
				pw.CloseWithError(fmt.Errorf("reading PHP-FPM's response: %w", err))
				return
			}
			length := int(binary.BigEndian.Uint16(header[4:6]))
			content := make([]byte, length+int(header[6]))
			if _, err := io.ReadFull(br, content); err != nil {
				pw.CloseWithError(fmt.Errorf("reading PHP-FPM's response: %w", err))
				return
			}
			switch header[1] {
			case fcgiStdout:
				if _, err := pw.Write(content[:length]); err != nil {
					return // nobody reads any more
				}
			case fcgiStderr:
				stderr.write(content[:length])
			case fcgiEndRequest:
				pw.Close()
				return
			}
		}
	}()
	return pr, stderr
}
