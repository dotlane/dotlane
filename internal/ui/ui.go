// Package ui formats what the CLI prints: one line per result, a marker, an aligned label and a
// dimmed detail. Colour is used only on a terminal, and never when NO_COLOR is set
// (https://no-color.org), so scripts and logs get plain text. CLICOLOR_FORCE=1 forces it, for
// pagers such as less -R.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
)

// labelWidth fits the longest step label, so details line up.
const labelWidth = 22

// Printer writes formatted output to one stream.
type Printer struct {
	w     io.Writer
	color bool
	home  string
}

// New returns a Printer for w, with colour if w is a terminal and NO_COLOR is unset.
func New(w io.Writer) *Printer {
	home, _ := os.UserHomeDir()
	return &Printer{w: w, color: wantColor(w), home: home}
}

func wantColor(w io.Writer) bool {
	switch {
	case os.Getenv("NO_COLOR") != "":
		return false
	case os.Getenv("CLICOLOR_FORCE") != "" && os.Getenv("CLICOLOR_FORCE") != "0":
		return true
	default:
		return isTerminal(w) && os.Getenv("TERM") != "dumb"
	}
}

// Plain returns a Printer without colour; for tests and non-terminal output.
func Plain(w io.Writer) *Printer { return &Printer{w: w} }

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (p *Printer) paint(code, s string) string {
	if !p.color || s == "" {
		return s
	}
	return code + s + reset
}

// Path shortens a path under the home directory to ~/…, for display.
func (p *Printer) Path(path string) string {
	if p.home != "" && strings.HasPrefix(path, p.home+"/") {
		return "~" + path[len(p.home):]
	}
	return path
}

// URL highlights a link.
func (p *Printer) URL(u string) string { return p.paint(cyan, u) }

// Dim de-emphasises secondary text.
func (p *Printer) Dim(s string) string { return p.paint(dim, s) }

// Title starts a section: a bold line, with an optional dimmed note, and a blank line after.
func (p *Printer) Title(title, note string) {
	line := p.paint(bold, title)
	if note != "" {
		line += "  " + p.paint(dim, note)
	}
	fmt.Fprintf(p.w, "%s\n\n", line)
}

func (p *Printer) step(mark, code, label, detail string) {
	fmt.Fprintf(p.w, "  %s %s %s\n", p.paint(code, mark), p.paint(bold, fmt.Sprintf("%-*s", labelWidth, label)), p.paint(dim, detail))
}

// OK reports a step that is done.
func (p *Printer) OK(label, detail string) { p.step("✓", green, label, detail) }

// Check reports a single finished action, without a label column.
func (p *Printer) Check(msg string) { fmt.Fprintf(p.w, "  %s %s\n", p.paint(green, "✓"), msg) }

// Skip reports a step deliberately left alone.
func (p *Printer) Skip(label, detail string) { p.step("·", dim, label, detail) }

// Warn reports a step that needs attention but did not fail.
func (p *Printer) Warn(label, detail string) { p.step("!", yellow, label, detail) }

// Ask announces a step that needs the administrator password, and why.
func (p *Printer) Ask(label, why string) {
	fmt.Fprintf(p.w, "\n  %s %s %s\n", p.paint(yellow, "›"), p.paint(bold, label), p.paint(yellow, "needs your password"))
	fmt.Fprintf(p.w, "    %s\n", why)
}

// Command shows a command exactly as it is about to run.
func (p *Printer) Command(cmd string) { fmt.Fprintf(p.w, "    %s\n", p.paint(dim, "$ "+cmd)) }

// Done ends a command with a highlighted outcome and an optional next step.
func (p *Printer) Done(outcome, next string) {
	fmt.Fprintf(p.w, "\n%s\n", p.paint(green+bold, outcome))
	if next != "" {
		fmt.Fprintf(p.w, "%s\n", next)
	}
}

// Line prints a plain line.
func (p *Printer) Line(format string, args ...any) { fmt.Fprintf(p.w, format+"\n", args...) }

// Error prints an error: a red marker, then the message; continuation lines are indented.
func (p *Printer) Error(err error) {
	msg := strings.ReplaceAll(err.Error(), "\n", "\n  ")
	fmt.Fprintf(p.w, "%s %s\n", p.paint(red+bold, "✗"), msg)
}

// SiteOK reports a site that was just added or updated.
func (p *Printer) SiteOK(url string, port int) {
	fmt.Fprintf(p.w, "  %s %s %s localhost:%d\n", p.paint(green, "✓"), p.URL(url), p.paint(dim, "→"), port)
}

// Site prints one site: its URL and where it forwards to, padded so a list lines up.
func (p *Printer) Site(url string, port, urlWidth int) {
	pad := urlWidth - len(url)
	if pad < 0 {
		pad = 0
	}
	fmt.Fprintf(p.w, "  %s%s  %s %s\n", p.URL(url), strings.Repeat(" ", pad), p.paint(dim, "→"), fmt.Sprintf("localhost:%d", port))
}
