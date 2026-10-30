package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestPlainOutput(t *testing.T) {
	var b bytes.Buffer
	p := New(&b) // a buffer is not a terminal: no colour
	p.Title("Installing Dotlane", "dev.dotlane")
	p.OK("Login service", "installed")
	p.Ask("DNS for .dldev", "Send lookups to Dotlane.")
	p.Command("sudo /bin/rm /etc/resolver/dldev")
	p.Error(errors.New("first\nsecond"))
	p.Site("https://a.dldev", 5173, 20)

	out := b.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape codes in non-terminal output:\n%q", out)
	}
	for _, want := range []string{
		"Installing Dotlane  dev.dotlane\n\n",
		"  ✓ Login service          installed\n",
		"  › DNS for .dldev needs your password\n    Send lookups to Dotlane.\n",
		"    $ sudo /bin/rm /etc/resolver/dldev\n",
		"✗ first\n  second\n",
		"  https://a.dldev       → localhost:5173\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestColour(t *testing.T) {
	var b bytes.Buffer
	p := &Printer{w: &b, color: true}
	p.OK("x", "y")
	if !strings.Contains(b.String(), green+"✓"+reset) {
		t.Errorf("no colour: %q", b.String())
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

func TestColourSettings(t *testing.T) {
	var b bytes.Buffer
	t.Setenv("CLICOLOR_FORCE", "1")
	if !New(&b).color {
		t.Error("CLICOLOR_FORCE=1 did not force colour")
	}
	t.Setenv("NO_COLOR", "1")
	if New(&b).color {
		t.Error("NO_COLOR did not win over CLICOLOR_FORCE")
	}
}
