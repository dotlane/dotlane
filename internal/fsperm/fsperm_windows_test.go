//go:build windows

package fsperm

import (
	"path/filepath"
	"testing"
)

func TestOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if ok, err := Owner(dir); err != nil || !ok {
		t.Errorf("Owner(PrivateDir) = %v, %v; want true", ok, err)
	}
	if ok, err := Owner(filepath.Dir(dir)); err != nil || ok {
		t.Errorf("Owner(a temp folder) = %v, %v; want false", ok, err)
	}
}
