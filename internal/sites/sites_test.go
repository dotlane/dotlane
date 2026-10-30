package sites

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	for _, s := range []Site{{Name: "a", Port: 1}, {Name: "myapp", Port: 5173}, {Name: "my-app2", Port: 65535}} {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}
	for _, s := range []Site{{Name: "", Port: 80}, {Name: "Bad", Port: 80}, {Name: "-a", Port: 80}, {Name: "a-", Port: 80}, {Name: "a.b", Port: 80}, {Name: "a_b", Port: 80}, {Name: "ok", Port: 0}, {Name: "ok", Port: 65536}} {
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
	for _, s := range []Site{{Name: "web", Port: 5173}, {Name: "api", Port: 8000}, {Name: "web", Port: 3000, Project: "/p/a"}} {
		if err := r.Put(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Put(Site{Name: "Bad", Port: 1}); err == nil {
		t.Error("invalid site accepted")
	}

	r2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := r2.List()
	want := []Site{{Name: "api", Port: 8000}, {Name: "web", Port: 3000, Project: "/p/a"}}
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

func TestCheckOwner(t *testing.T) {
	byHand := Site{Name: "web", Port: 1}
	mine := Site{Name: "web", Port: 2, Project: "/p/a"}
	theirs := Site{Name: "web", Port: 3, Project: "/p/b"}
	for _, tc := range []struct {
		existing, s Site
		ok          bool
	}{
		{byHand, mine, true},   // dotlane up adopts a site added by hand
		{byHand, byHand, true}, // dotlane add updates its own
		{mine, mine, true},     // the same project
		{mine, theirs, false},  // another project
		{mine, byHand, false},  // dotlane add over a project's site
	} {
		err := CheckOwner(tc.existing, tc.s)
		var conflict *ConflictError
		if tc.ok != (err == nil) || (err != nil && !errors.As(err, &conflict)) {
			t.Errorf("%+v over %+v: %v", tc.s, tc.existing, err)
		}
	}
	if err := CheckOwner(mine, theirs); !strings.Contains(err.Error(), "/p/a") {
		t.Errorf("message does not name the owner: %v", err)
	}
}
