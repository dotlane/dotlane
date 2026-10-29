package sites

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, s := range []Site{{"a", 1}, {"myapp", 5173}, {"my-app2", 65535}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	for _, s := range []Site{{"", 80}, {"Bad", 80}, {"-a", 80}, {"a-", 80}, {"a.b", 80}, {"a_b", 80}, {"ok", 0}, {"ok", 65536}} {
		var invalid *InvalidError
		if err := s.Validate(); !errors.As(err, &invalid) {
			t.Errorf("%+v: got %v, want InvalidError", s, err)
		}
	}
}

func TestRegistryPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []Site{{"web", 5173}, {"api", 8000}, {"web", 3000}} {
		if err := r.Put(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Put(Site{"Bad", 1}); err == nil {
		t.Error("invalid site accepted")
	}

	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := r2.List()
	want := []Site{{"api", 8000}, {"web", 3000}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reopened = %v, want %v (sorted, last port wins)", got, want)
	}

	if err := r2.Delete("api"); err != nil {
		t.Fatal(err)
	}
	if err := r2.Delete("api"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v, want ErrNotFound", err)
	}
	if _, ok := r2.Get("api"); ok {
		t.Error("deleted site still there")
	}

	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("leftover temporary files: %v", entries)
	}
}

func TestOpenRejectsBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sites.json")
	os.WriteFile(path, []byte(`{"version":1,"sites":[{"name":"Bad","port":1}]}`), 0o600)
	if _, err := Open(path); err == nil {
		t.Error("invalid site in file accepted")
	}
}
