package tld

import (
	"strings"
	"testing"
)

func TestCheck(t *testing.T) {
	for _, tc := range []struct {
		tld, err, warning string
	}{
		// Fine: the suggested endings, and private ones like the old default.
		{tld: "dotlane"},
		{tld: "test"},
		{tld: "internal"},
		{tld: "dotlane"},
		{tld: "dldev"},
		{tld: "my-company"},

		// Not a label.
		{tld: "Bad", err: "lowercase"},
		{tld: "a.b", err: "lowercase"},
		{tld: "1abc", err: "lowercase"},
		{tld: "abc-", err: "lowercase"},
		{tld: "", err: "lowercase"},
		{tld: strings.Repeat("a", 64), err: "lowercase"},

		// Special-use names.
		{tld: "local", err: "multicast DNS"},
		{tld: "localhost", err: "RFC 6761"},
		{tld: "invalid", err: "never resolve"},
		{tld: "onion", err: "Tor"},
		{tld: "alt", err: "RFC 9476"},
		{tld: "arpa", err: "reverse DNS"},

		// Public, or may become public.
		{tld: "com", err: "public top-level domain"},
		{tld: "dev", err: "public top-level domain"},
		{tld: "app", err: "public top-level domain"},
		{tld: "pt", err: "public top-level domain"},
		{tld: "dl", err: "country codes"},
		{tld: "zz", err: "country codes"},
		{tld: "xn--abc", err: "internationalized"},

		// Works, but the network may answer it already.
		{tld: "lan", warning: "routers"},
		{tld: "home", warning: "routers"},
		{tld: "corp", warning: "routers"},
		{tld: "localdomain", warning: "routers"},
	} {
		warning, err := Check(tc.tld)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%q: unexpected error %v", tc.tld, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%q: error %v, want it to mention %q", tc.tld, err, tc.err)
		case tc.warning == "" && warning != "":
			t.Errorf("%q: unexpected warning %q", tc.tld, warning)
		case tc.warning != "" && !strings.Contains(warning, tc.warning):
			t.Errorf("%q: warning %q, want it to mention %q", tc.tld, warning, tc.warning)
		}
	}
}

func TestSuggestedPass(t *testing.T) {
	for _, s := range Suggested {
		if warning, err := Check(s); warning != "" || err != nil {
			t.Errorf("suggested %q: %q, %v", s, warning, err)
		}
	}
}

// The embedded list is IANA's, not empty or truncated.
func TestDelegatedList(t *testing.T) {
	if !strings.HasPrefix(ianaList, "# Version ") {
		t.Fatalf("embedded list does not start with IANA's version line: %.40q", ianaList)
	}
	if n := len(delegated()); n < 1000 {
		t.Errorf("%d delegated TLDs, want over 1,000", n)
	}
}
