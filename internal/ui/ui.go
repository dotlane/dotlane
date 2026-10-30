// Package ui formats what the CLI prints: one line per result, a marker, an aligned label and a
// dimmed detail.
//
// Styles come from Lip Gloss, with colours chosen for the terminal's light or dark background.
// Output goes through a colour-profile writer, so colours are downsampled to what the terminal
// supports and dropped entirely when the output is not a terminal or NO_COLOR is set
// (https://no-color.org); CLICOLOR_FORCE=1 forces them, for pagers such as less -R.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"golang.org/x/term"
)

// labelWidth fits the longest step label, so details line up.
const labelWidth = 22

type styles struct {
	bold, dim, url, title                  lipgloss.Style
	ok, skip, warn, fail, ask, done, badge lipgloss.Style
}

func newStyles(dark bool) styles {
	c := lipgloss.LightDark(dark)
	green := c(lipgloss.Color("#1A7F37"), lipgloss.Color("#3FB950"))
	amber := c(lipgloss.Color("#9A6700"), lipgloss.Color("#E3B341"))
	return styles{
		bold:  lipgloss.NewStyle().Bold(true),
		dim:   lipgloss.NewStyle().Foreground(c(lipgloss.Color("#6E7781"), lipgloss.Color("#8B949E"))),
		url:   lipgloss.NewStyle().Foreground(c(lipgloss.Color("#0969DA"), lipgloss.Color("#58C4DC"))),
		title: lipgloss.NewStyle().Bold(true),
		ok:    lipgloss.NewStyle().Foreground(green),
		skip:  lipgloss.NewStyle().Foreground(c(lipgloss.Color("#8C959F"), lipgloss.Color("#6E7681"))),
		warn:  lipgloss.NewStyle().Foreground(amber),
		fail:  lipgloss.NewStyle().Foreground(c(lipgloss.Color("#CF222E"), lipgloss.Color("#FF7B72"))),
		ask:   lipgloss.NewStyle().Foreground(amber),
		done:  lipgloss.NewStyle().Bold(true).Foreground(green),
		badge: lipgloss.NewStyle().Bold(true).Padding(0, 1).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(c(lipgloss.Color("#CF222E"), lipgloss.Color("#DA3633"))),
	}
}

// The background is asked of the terminal once per process; when stdin or stdout is not a
// terminal there is nobody to ask, and dark is assumed.
var darkBackground = sync.OnceValue(func() bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return true
	}
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
})

// Printer writes formatted output to one stream.
type Printer struct {
	w    io.Writer
	s    styles
	home string
}

// New returns a Printer for w, with colour when w is a terminal (see the package comment).
func New(w io.Writer) *Printer {
	home, _ := os.UserHomeDir()
	cw := colorprofile.NewWriter(w, os.Environ())
	// NO_COLOR always wins (no-color.org); colorprofile lets CLICOLOR_FORCE override it when the
	// output is not a terminal. Bold and dim stay, as NO_COLOR intends.
	if os.Getenv("NO_COLOR") != "" && cw.Profile > colorprofile.ASCII {
		cw.Profile = colorprofile.ASCII
	}
	return &Printer{w: cw, s: newStyles(darkBackground()), home: home}
}

// Plain returns a Printer without colour; for tests.
func Plain(w io.Writer) *Printer {
	return &Printer{w: &colorprofile.Writer{Forward: w, Profile: colorprofile.NoTTY}, s: newStyles(true)}
}

func (p *Printer) printf(format string, args ...any) { fmt.Fprintf(p.w, format, args...) }

// Path shortens a path under the home directory to ~/…, for display.
func (p *Printer) Path(path string) string {
	if p.home != "" && strings.HasPrefix(path, p.home+"/") {
		return "~" + path[len(p.home):]
	}
	return path
}

// URL highlights a link.
func (p *Printer) URL(u string) string { return p.s.url.Render(u) }

// Dim de-emphasises secondary text.
func (p *Printer) Dim(s string) string { return p.s.dim.Render(s) }

// Title starts a section: a bold line, with an optional dimmed note, and a blank line after.
func (p *Printer) Title(title, note string) {
	line := p.s.title.Render(title)
	if note != "" {
		line += "  " + p.s.dim.Render(note)
	}
	p.printf("%s\n\n", line)
}

func (p *Printer) step(mark string, style lipgloss.Style, label, detail string) {
	p.printf("  %s %s %s\n", style.Render(mark), p.s.bold.Width(labelWidth).Render(label), p.s.dim.Render(detail))
}

// OK reports a step that is done.
func (p *Printer) OK(label, detail string) { p.step("✓", p.s.ok, label, detail) }

// Check reports a single finished action, without a label column.
func (p *Printer) Check(msg string) { p.printf("  %s %s\n", p.s.ok.Render("✓"), msg) }

// Skip reports a step deliberately left alone.
func (p *Printer) Skip(label, detail string) { p.step("·", p.s.skip, label, detail) }

// Warn reports a step that needs attention but did not fail.
func (p *Printer) Warn(label, detail string) { p.step("!", p.s.warn, label, detail) }

// Fail reports a step that is broken.
func (p *Printer) Fail(label, detail string) { p.step("✗", p.s.fail, label, detail) }

// Hint shows how to fix the step above it.
func (p *Printer) Hint(fix string) {
	p.printf("    %s %s\n", p.s.dim.Render("fix:"), strings.ReplaceAll(fix, "\n", "\n         "))
}

// Ask announces a step that needs the administrator password, and why.
func (p *Printer) Ask(label, why string) {
	p.printf("\n  %s %s %s\n", p.s.ask.Render("›"), p.s.bold.Render(label), p.s.ask.Render("needs your password"))
	p.printf("    %s\n", why)
}

// Command shows a command exactly as it is about to run.
func (p *Printer) Command(cmd string) { p.printf("    %s\n", p.s.dim.Render("$ "+cmd)) }

// Done ends a command with a highlighted outcome and an optional next step.
func (p *Printer) Done(outcome, next string) {
	p.printf("\n%s\n", p.s.done.Render(outcome))
	if next != "" {
		p.printf("%s\n", next)
	}
}

// Line prints a plain line.
func (p *Printer) Line(format string, args ...any) { p.printf(format+"\n", args...) }

// Error prints an error under a red ERROR badge; every line of the message is indented.
func (p *Printer) Error(err error) {
	msg := "  " + strings.ReplaceAll(err.Error(), "\n", "\n  ")
	p.printf("%s\n%s\n", p.s.badge.Render("ERROR"), msg)
}

// SiteOK reports a site that was just added or updated.
func (p *Printer) SiteOK(url string, port int) {
	p.printf("  %s %s %s localhost:%d\n", p.s.ok.Render("✓"), p.URL(url), p.s.dim.Render("→"), port)
}

// SiteRow is one line of SiteTable. Name may be empty to continue the previous site.
type SiteRow struct {
	Name string
	URL  string
	Port int
}

// SiteTable prints sites as a table: a header, a rule under it, no other borders.
func (p *Printer) SiteTable(rows []SiteRow) {
	t := table.New().
		Headers("SITE", "URL", "FORWARDS TO").
		Border(lipgloss.NormalBorder()).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).
		BorderStyle(p.s.dim).
		StyleFunc(func(row, col int) lipgloss.Style {
			cell := lipgloss.NewStyle().PaddingLeft(2)
			switch {
			case row == table.HeaderRow:
				return cell.Inherit(p.s.dim).Bold(true)
			case col == 0:
				return cell.Inherit(p.s.bold)
			case col == 1:
				return cell.Inherit(p.s.url)
			}
			return cell
		})
	for _, r := range rows {
		t.Row(r.Name, r.URL, fmt.Sprintf("localhost:%d", r.Port))
	}
	p.printf("%s\n", t.Render())
}
