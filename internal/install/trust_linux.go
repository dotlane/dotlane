//go:build linux

package install

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// TrustStore is one distribution family's way of adding a certificate authority to the system.
type TrustStore struct {
	Tool string // the command that rebuilds the trust list from Dir
	Dir  string // where authorities are dropped
	// Create means Dir may be missing and should be made (Debian's is not there until the first
	// local certificate).
	Create bool
	// Fresh is an argument that makes Tool drop entries whose file is gone.
	Fresh string
	// NSSPackage is the package that has certutil.
	NSSPackage string
}

// defaultStores are the families, found by the tool and folder each has: Debian and Ubuntu,
// Arch, then Fedora and RHEL. Arch ships update-ca-trust too, with its own anchors folder.
func defaultStores() []TrustStore {
	return []TrustStore{
		{Tool: "update-ca-certificates", Dir: "/usr/local/share/ca-certificates", Create: true, Fresh: "--fresh", NSSPackage: "libnss3-tools"},
		{Tool: "update-ca-trust", Dir: "/etc/ca-certificates/trust-source/anchors", NSSPackage: "nss"},
		{Tool: "update-ca-trust", Dir: "/etc/pki/ca-trust/source/anchors", NSSPackage: "nss-tools"},
	}
}

// defaultBundles are the files programs read the system's authorities from (the ones Go's
// crypto/x509 looks at, in the same order).
func defaultBundles() []string {
	return []string{
		"/etc/ssl/certs/ca-certificates.crt",                // Debian, Ubuntu, Arch
		"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora, RHEL
		"/etc/ssl/ca-bundle.pem",                            // openSUSE
		"/etc/pki/tls/cacert.pem",                           // OpenELEC
		"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS, RHEL 7
		"/etc/ssl/cert.pem",                                 // Alpine
	}
}

// store is the trust store of this machine.
func (m *Linux) store() (TrustStore, error) {
	for _, s := range m.Stores {
		if !m.has(s.Tool) {
			continue
		}
		if _, err := os.Stat(s.Dir); err == nil || s.Create {
			return s, nil
		}
	}
	return TrustStore{}, errors.New("no system trust store found: expected update-ca-certificates (Debian, Ubuntu) " +
		"or update-ca-trust (Fedora, RHEL, Arch); install the ca-certificates package")
}

// anchorPath is where this service's authority goes in s.
func (m *Linux) anchorPath(s TrustStore) string {
	return filepath.Join(s.Dir, "dotlane-"+m.Cfg.ServiceName+".crt")
}

// caTrusted reports whether the system's trust list vouches for Dotlane's root. It verifies the
// certificate against the lists the trust tools write, read afresh: x509.SystemCertPool keeps the
// pool it loaded first, so a process that adds the authority would never see it.
func (m *Linux) caTrusted() bool {
	data, err := os.ReadFile(m.rootCert())
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	pool := x509.NewCertPool()
	for _, b := range m.Bundles {
		if list, err := os.ReadFile(b); err == nil {
			pool.AppendCertsFromPEM(list)
		}
	}
	_, err = cert.Verify(x509.VerifyOptions{Roots: pool})
	return err == nil
}

// nssNickname names the authority in the NSS databases.
func (m *Linux) nssNickname() string {
	return "Dotlane Local Authority (" + m.Cfg.ServiceName + ")"
}

// nssDirs are the NSS databases browsers read: Chromium's shared one, which is created when
// missing (create), and every Firefox profile that has one.
func (m *Linux) nssDirs(create bool) []string {
	var dirs []string
	shared := filepath.Join(m.Home, ".pki", "nssdb")
	if _, err := os.Stat(filepath.Join(shared, "cert9.db")); err == nil || create {
		dirs = append(dirs, shared)
	}
	for _, g := range m.FirefoxGlobs {
		matches, _ := filepath.Glob(filepath.Join(m.Home, g, "cert9.db"))
		for _, db := range matches {
			dirs = append(dirs, filepath.Dir(db))
		}
	}
	return dirs
}

// nssHolds reports whether the database in dir has Dotlane's current root under its nickname.
func (m *Linux) nssHolds(ctx context.Context, dir string) bool {
	out, err := m.Runner.Run(ctx, m.tool("certutil"), "-L", "-d", "sql:"+dir, "-n", m.nssNickname(), "-a")
	if err != nil {
		return false
	}
	have, _ := pem.Decode(out)
	want, _ := m.rootDER()
	return have != nil && want != nil && bytes.Equal(have.Bytes, want)
}

func (m *Linux) rootDER() ([]byte, error) {
	data, err := os.ReadFile(m.rootCert())
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%s is not a PEM certificate", m.rootCert())
	}
	return block.Bytes, nil
}

func (m *Linux) trustCA(ctx context.Context) error {
	// The daemon creates the CA when it first starts; give it a moment.
	for i := 0; ; i++ {
		if _, err := os.Stat(m.rootCert()); err == nil {
			break
		}
		if i == 50 {
			return fmt.Errorf("the service has not created its certificate authority (%s); see: %s", m.rootCert(), m.journalCmd())
		}
		time.Sleep(100 * time.Millisecond)
	}
	store, err := m.store()
	if err != nil {
		return err
	}
	if err := m.trustSystem(ctx, store); err != nil {
		return err
	}
	return m.trustBrowsers(ctx, store)
}

func (m *Linux) trustSystem(ctx context.Context, store TrustStore) error {
	if m.caTrusted() {
		m.UI.OK("Certificate authority", "already trusted")
		return nil
	}
	m.UI.Ask("Certificate authority",
		"Trusts Dotlane's local certificate authority, so browsers and tools accept its HTTPS\n"+
			"    certificates. Added to the system's trust store; dotlane uninstall removes it.")
	data, err := os.ReadFile(m.rootCert())
	if err != nil {
		return err
	}
	if _, err := os.Stat(store.Dir); err != nil {
		if err := m.sudo(ctx, nil, m.tool("mkdir"), "-p", store.Dir); err != nil {
			return err
		}
	}
	if err := m.sudo(ctx, data, m.tool("tee"), m.anchorPath(store)); err != nil {
		return err
	}
	if err := m.sudo(ctx, nil, m.tool(store.Tool)); err != nil {
		return err
	}
	if !m.caTrusted() {
		return fmt.Errorf("the certificate authority was added (%s) but %s did not make the system trust it", m.anchorPath(store), store.Tool)
	}
	m.UI.OK("Certificate authority", "trusted")
	return nil
}

// trustBrowsers adds the root to the NSS databases of Chromium and Firefox, which ignore the
// system store. No administrator rights are needed: they are the user's files.
func (m *Linux) trustBrowsers(ctx context.Context, store TrustStore) error {
	if !m.has("certutil") {
		m.UI.Warn("Browsers", fmt.Sprintf("Firefox and Chromium keep their own list of authorities, and need certutil to add\n"+
			"    Dotlane's. Install the %s package, then run dotlane install again.", store.NSSPackage))
		return nil
	}
	added := 0
	for _, dir := range m.nssDirs(true) {
		if m.nssHolds(ctx, dir) {
			continue
		}
		if err := m.addToNSS(ctx, dir); err != nil {
			return err
		}
		added++
	}
	if added == 0 {
		m.UI.OK("Browsers", "Firefox and Chromium already trust it")
	} else {
		m.UI.OK("Browsers", fmt.Sprintf("trusted in %d Firefox and Chromium databases (restart the browser)", added))
	}
	return nil
}

func (m *Linux) addToNSS(ctx context.Context, dir string) error {
	certutil := m.tool("certutil")
	db := "sql:" + dir
	if _, err := os.Stat(filepath.Join(dir, "cert9.db")); err != nil {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if out, err := m.Runner.Run(ctx, certutil, "-N", "-d", db, "--empty-password"); err != nil {
			return fmt.Errorf("certutil -N %s: %v: %s", m.UI.Path(dir), err, bytes.TrimSpace(out))
		}
	}
	// A different root under the same nickname is a certificate authority from before a purge.
	m.Runner.Run(ctx, certutil, "-D", "-d", db, "-n", m.nssNickname())
	if out, err := m.Runner.Run(ctx, certutil, "-A", "-d", db, "-n", m.nssNickname(), "-t", "C,,", "-i", m.rootCert()); err != nil {
		return fmt.Errorf("certutil -A %s: %v: %s", m.UI.Path(dir), err, bytes.TrimSpace(out))
	}
	return nil
}

// untrustCA removes the root from the system store, whichever families' folder holds it, and
// from the browsers' databases.
func (m *Linux) untrustCA(ctx context.Context) error {
	removed := false
	for _, s := range m.Stores {
		path := m.anchorPath(s)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if !removed {
			m.UI.Ask("Certificate authority",
				"Removes Dotlane's certificate authority from the system's trust store, so its\n"+
					"    certificates are no longer trusted.")
		}
		if err := m.sudo(ctx, nil, m.tool("rm"), path); err != nil {
			return err
		}
		args := []string{}
		if s.Fresh != "" {
			args = append(args, s.Fresh)
		}
		if err := m.sudo(ctx, nil, m.tool(s.Tool), args...); err != nil {
			return err
		}
		removed = true
	}
	if removed {
		m.UI.OK("Certificate authority", "removed from the system's trust store")
	} else {
		m.UI.Skip("Certificate authority", "not in the system's trust store")
	}

	if !m.has("certutil") {
		if len(m.nssDirs(false)) > 0 {
			m.UI.Warn("Browsers", "certutil is not installed, so Dotlane's authority stays in the browsers' databases;\n"+
				"    remove \""+m.nssNickname()+"\" in the browser's certificate settings")
		}
		return nil
	}
	n := 0
	for _, dir := range m.nssDirs(false) {
		if _, err := m.Runner.Run(ctx, m.tool("certutil"), "-L", "-d", "sql:"+dir, "-n", m.nssNickname()); err != nil {
			continue // not in this one
		}
		if out, err := m.Runner.Run(ctx, m.tool("certutil"), "-D", "-d", "sql:"+dir, "-n", m.nssNickname()); err != nil {
			return fmt.Errorf("certutil -D %s: %v: %s", m.UI.Path(dir), err, bytes.TrimSpace(out))
		}
		n++
	}
	if n > 0 {
		m.UI.OK("Browsers", fmt.Sprintf("removed from %d Firefox and Chromium databases", n))
	} else {
		m.UI.Skip("Browsers", "not in any Firefox or Chromium database")
	}
	return nil
}
