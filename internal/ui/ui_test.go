package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestPlainOutput(t *testing.T) {
	var b bytes.Buffer
	p := New(&b) // a buffer is not a terminal: no colour
	p.Title("Installing Dotlane", "dev.dotlane")
	p.OK("Login service", "installed")
	p.Ask("DNS for .dldev", "Send lookups to Dotlane.")
	p.Command("sudo /bin/rm /etc/resolver/dldev")
	p.Error(errors.New("first\nsecond"))
	p.SiteOK("https://a.dldev", 5173)

	out := b.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes in non-terminal output:\n%q", out)
	}
	for _, want := range []string{
		"Installing Dotlane  dev.dotlane\n\n",
		"  ✓ Login service          installed\n",
		"  › DNS for .dldev needs your password\n    Send lookups to Dotlane.\n",
		"    $ sudo /bin/rm /etc/resolver/dldev\n",
		" ERROR \n  first\n  second\n",
		"  ✓ https://a.dldev → localhost:5173\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestSiteTable(t *testing.T) {
	var b bytes.Buffer
	Plain(&b).SiteTable([]SiteRow{
		{"api", "https://api.dldev:9443", 8000, ""},
		{"myapp", "https://myapp.dldev:9443", 5173, "/p/myapp"},
		{"", "https://myapp.test:9443", 5173, ""},
	})
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("want header, rule and 3 rows, got:\n%s", b.String())
	}
	if !strings.Contains(lines[0], "SITE") || !strings.Contains(lines[0], "FORWARDS TO") || !strings.Contains(lines[3], "/p/myapp") || !strings.Contains(lines[1], "─") {
		t.Errorf("header:\n%s", b.String())
	}
	// Columns line up: every row has its URL at the same offset.
	at := strings.Index(lines[2], "https://")
	for _, l := range lines[3:] {
		if strings.Index(l, "https://") != at {
			t.Errorf("misaligned:\n%s", b.String())
		}
	}
}

func TestColour(t *testing.T) {
	var b bytes.Buffer
	p := &Printer{w: &colorprofile.Writer{Forward: &b, Profile: colorprofile.TrueColor}, s: newStyles(true)}
	p.OK("x", "y")
	if !strings.Contains(b.String(), "\x1b[") {
		t.Errorf("no colour on a colour profile: %q", b.String())
	}
}

func TestColourSettings(t *testing.T) {
	var b bytes.Buffer
	t.Setenv("CLICOLOR_FORCE", "1")
	New(&b).OK("x", "y")
	if !strings.Contains(b.String(), "\x1b[") {
		t.Errorf("CLICOLOR_FORCE=1 did not force colour: %q", b.String())
	}
	b.Reset()
	t.Setenv("NO_COLOR", "1")
	New(&b).OK("x", "y")
	if strings.Contains(b.String(), "\x1b[3") {
		t.Errorf("NO_COLOR left colours in: %q", b.String())
	}
}

func TestPath(t *testing.T) {
	p := &Printer{home: "/Users/me"}
	if got := p.Path("/Users/me/Library/x"); got != "~/Library/x" {
		t.Errorf("Path = %q", got)
	}
	if got := p.Path("/Users/meme/x"); got != "/Users/meme/x" {
		t.Errorf("Path = %q (must not match a prefix of another name)", got)
	}
}
