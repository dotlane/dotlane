package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"charm.land/fang/v2"
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
