package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"charm.land/fang/v2"

	"github.com/dotlane/dotlane/internal/api"
)

// Every command's help renders through Fang. Fang parses examples as shell commands, and an
// example it cannot parse made `dotlane up --help` panic.
func TestHelp(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, c := range newRoot().Commands() {
		if c.Hidden {
			continue
		}
		name := c.Name()
		t.Run(name, func(t *testing.T) {
			root := newRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{name, "--help"})
			if err := fang.Execute(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "USAGE") {
				t.Errorf("no usage:\n%s", out.String())
			}
		})
	}
}

func TestUsageHint(t *testing.T) {
	err := withUsageHint(errorString("accepts 2 arg(s), received 1"))
	if !strings.HasSuffix(err.Error(), "try --help for usage") {
		t.Errorf("got %q", err)
	}
	if err := withUsageHint(errorString("no such site: x")); strings.Contains(err.Error(), "--help") {
		t.Errorf("hint on a non-usage error: %q", err)
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }

// An old command on the PATH and a newer service, or the other way round: a different API fails
// and names the side to update; a newer release with the same API only warns.
func TestCompatible(t *testing.T) {
	for _, tc := range []struct {
		name        string
		st          api.Status
		cli         string
		warn, fails string
	}{
		{name: "same", st: api.Status{Version: "0.2.0", API: api.Version}, cli: "0.2.0"},
		{name: "older service", st: api.Status{Version: "0.1.0", API: api.Version}, cli: "0.2.0"},
		{name: "service from before api", st: api.Status{Version: "0.1.0"}, cli: "0.2.0"},
		{name: "dev build", st: api.Status{Version: "0.3.0", API: api.Version}, cli: "dev"},
		{name: "newer service", st: api.Status{Version: "0.3.0", API: api.Version}, cli: "0.2.0", warn: "older than the service (0.3.0)"},
		{name: "newer api", st: api.Status{Version: "0.3.0", API: api.Version + 1}, cli: "0.2.0", fails: "too old for the Dotlane service"},
		{name: "older api", st: api.Status{Version: "0.3.0", API: -1}, cli: "dev", fails: "dotlane install --refresh"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			warn, err := compatible(tc.st, tc.cli)
			if tc.fails != "" {
				if err == nil || !strings.Contains(err.Error(), tc.fails) {
					t.Errorf("err = %v, want %q", err, tc.fails)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (tc.warn == "") != (warn == "") || !strings.Contains(warn, tc.warn) {
				t.Errorf("warning = %q, want %q", warn, tc.warn)
			}
		})
	}
}
