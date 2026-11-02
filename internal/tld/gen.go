//go:build ignore

// Gen refreshes tlds-alpha-by-domain.txt, IANA's list of delegated top-level domains, which
// package tld embeds to refuse endings that would shadow public websites:
//
//	go generate ./internal/tld
package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"time"
)

const source = "https://data.iana.org/TLD/tlds-alpha-by-domain.txt"

var line = regexp.MustCompile(`^[A-Z0-9-]+$`)

func main() {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("%s: %s", source, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}

	// Refuse anything that is not the list, so a captive portal or an error page never
	// replaces it.
	lines := bytes.Split(bytes.TrimSpace(body), []byte("\n"))
	if len(lines) == 0 || !bytes.HasPrefix(lines[0], []byte("# Version ")) {
		log.Fatalf("%s: no version line; not IANA's list", source)
	}
	for i, l := range lines[1:] {
		if !line.Match(l) {
			log.Fatalf("%s: line %d is not a TLD: %q", source, i+2, l)
		}
	}
	if n := len(lines) - 1; n < 1000 {
		log.Fatalf("%s: only %d TLDs; expected over 1,000", source, n)
	}

	if err := os.WriteFile("tlds-alpha-by-domain.txt", body, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d TLDs\n", lines[0], len(lines)-1)
}
