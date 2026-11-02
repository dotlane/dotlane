// Package tld decides which domain endings Dotlane may route to this machine.
//
// Endings fall into three tiers ([docs/tld-management.md]): refused, because they cannot work
// or would shadow public websites; warned, because a router or company network often answers
// them already; and the rest, which are fine. Suggested lists the ones that can never collide.
//
// [docs/tld-management.md]: https://github.com/dotlane/dotlane/blob/main/docs/tld-management.md
package tld

//go:generate go run gen.go

import (
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Suggested are endings that cannot collide with a public website: dotlane is the default,
// test is reserved by RFC 2606, internal by ICANN for private use (2024).
var Suggested = []string{"dotlane", "test", "internal"}

var pattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,61}[a-z0-9]$`)

// special are names that resolvers, browsers or the OS treat specially, with why.
var special = map[string]string{
	"local":     "it belongs to multicast DNS (Bonjour, Avahi)",
	"localhost": "browsers and the OS answer it themselves with loopback (RFC 6761), so Dotlane's DNS is never asked",
	"invalid":   "it must never resolve (RFC 6761)",
	"onion":     "it belongs to Tor (RFC 7686)",
	"alt":       "it is reserved for name systems other than DNS (RFC 9476)",
	"arpa":      "it is for reverse DNS and internet infrastructure",
}

// shared are endings that routers (OpenWrt, ISP boxes) and company DNS often answer.
var shared = map[string]bool{
	"lan": true, "home": true, "corp": true, "mail": true,
	"localdomain": true, "intranet": true, "private": true,
}

//go:embed tlds-alpha-by-domain.txt
var ianaList string

// delegated is IANA's list of public top-level domains, lowercased.
var delegated = sync.OnceValue(func() map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(ianaList, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			m[strings.ToLower(l)] = true
		}
	}
	return m
})

// Syntax reports whether t can be a domain ending at all: one DNS label of lowercase letters,
// digits and dashes, starting with a letter.
func Syntax(t string) error {
	if !pattern.MatchString(t) {
		return fmt.Errorf("domain ending must be lowercase letters, digits and dashes: %q", t)
	}
	return nil
}

// Check judges t as an ending to route to this machine. It returns an error when Dotlane
// refuses t, and a warning when t works but may hide names the network already answers.
func Check(t string) (warning string, err error) {
	if err := Syntax(t); err != nil {
		return "", err
	}
	switch {
	case special[t] != "":
		return "", fmt.Errorf("domain ending cannot be %s: %s", t, special[t])
	case strings.HasPrefix(t, "xn--"):
		return "", fmt.Errorf("domain ending cannot be %s: internationalized endings are not supported", t)
	case delegated()[t]:
		return "", fmt.Errorf("domain ending cannot be %s: it is a public top-level domain, so routing it here would hide every real .%s website on this machine", t, t)
	case len(t) == 2:
		return "", fmt.Errorf("domain ending cannot be %s: two-letter endings are kept for country codes and may be assigned at any time", t)
	case shared[t]:
		return fmt.Sprintf(".%s is often answered by routers and company networks; Dotlane would hide the network's own .%s names", t, t), nil
	}
	return "", nil
}
