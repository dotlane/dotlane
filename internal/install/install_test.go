package install

import "testing"

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		version, running string
		want             bool
	}{
		{"0.2.0", "0.1.0", true},
		{"v0.2.0", "0.1.0", true},
		{"0.1.0", "0.1.0-rc.2", true}, // a release after its candidate
		{"0.1.0-rc.2", "0.1.0-rc.1", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false}, // never a downgrade
		{"0.1.0-rc.2", "0.1.0", false},
		// Development builds never replace a release, nor are replaced by one.
		{"dev", "0.1.0", false},
		{"0.2.0", "dev", false},
		{"v0.0.0-20261030172955-9c941e1f9f7e", "0.1.0", false},
		{"v0.1.0-rc.2.0.20261031142953-7db30294ff51", "0.1.0-rc.2", false},
		{"v0.1.0-rc.2.0.20261031142953-7db30294ff51+dirty", "0.1.0-rc.2", false},
		{"0.2.0", "v0.0.0-20261030172955-9c941e1f9f7e", false},
		{"0.2.0", "", false},
		{"0.2.0", "test", false},
	} {
		if got := Newer(tc.version, tc.running); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.version, tc.running, got, tc.want)
		}
	}
}
