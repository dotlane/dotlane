package fsperm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "dotlane")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(dir); err != nil { // again: already there
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s: %v", dir, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", info.Mode().Perm())
	}
	f := filepath.Join(dir, "root.key")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PrivateFile(f); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(f); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", info.Mode().Perm())
	}
}
