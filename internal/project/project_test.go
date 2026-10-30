package project

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dotlane/dotlane/internal/sites"
)

func write(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir()) // /var is a symlink on macOS
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFind(t *testing.T) {
	root := realTempDir(t)
	want := write(t, root, "[sites.a]\nport = 1\n")
	deep := filepath.Join(root, "web", "src")
	os.MkdirAll(deep, 0o755)
	for _, dir := range []string{root, deep} {
		if got, err := Find(dir); err != nil || got != want {
			t.Errorf("Find(%s) = %s, %v", dir, got, err)
		}
	}
	if _, err := Find(t.TempDir()); !errors.Is(err, ErrNotFound) {
		t.Errorf("no file: %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := realTempDir(t)
	p, err := Load(write(t, dir, `# the web app and its API
[sites.web]
port = 5173

[sites.api]
port = 8000   # FastAPI
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []sites.Site{{Name: "api", Port: 8000, Project: dir}, {Name: "web", Port: 5173, Project: dir}}
	if p.Dir != dir || !slices.Equal(p.Sites, want) {
		t.Errorf("Load = %+v", p)
	}

	// Through a symlink, the project is still the real directory.
	link := filepath.Join(realTempDir(t), "link")
	os.Symlink(dir, link)
	if p, err := Load(filepath.Join(link, FileName)); err != nil || p.Dir != dir {
		t.Errorf("via symlink: %+v, %v", p, err)
	}

	for _, tc := range []struct{ content, want string }{
		{"", "lists no sites"},
		{"[sites.web]\nport = 0\n", "not a port"},
		{"[sites.Web]\nport = 80\n", "lowercase"},
		{"[sites.web]\nprot = 80\n", "prot"},
		{"tld = \"test\"\n", "tld"},
		{"[sites.web]\nport = 1\n[sites.web]\nport = 2\n", "web"},
	} {
		if _, err := Load(write(t, t.TempDir(), tc.content)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: %v, want %q", tc.content, err, tc.want)
		}
	}
}

func TestUpAndDown(t *testing.T) {
	p := &Project{Dir: "/p/a", Sites: []sites.Site{
		{Name: "api", Port: 8000, Project: "/p/a"},
		{Name: "shared", Port: 3000, Project: "/p/a"},
		{Name: "web", Port: 5173, Project: "/p/a"},
	}}
	registered := []sites.Site{
		{Name: "api", Port: 8001, Project: "/p/a"},  // ours, port changes
		{Name: "docs", Port: 4000, Project: "/p/a"}, // ours, gone from the file
		{Name: "other", Port: 9000, Project: "/p/b"},
		{Name: "shared", Port: 3001, Project: "/p/b"}, // the other project has it
		{Name: "web", Port: 5173},                     // added by hand: up adopts it
	}
	conflicts, stale := p.Up(registered)
	if len(conflicts) != 1 || conflicts[0].Name != "shared" || conflicts[0].Owner != "/p/b" {
		t.Errorf("conflicts = %+v", conflicts)
	}
	if !slices.Equal(stale, []string{"docs"}) {
		t.Errorf("stale = %v", stale)
	}
	if got := p.Down(registered); !slices.Equal(got, []string{"api", "docs"}) {
		t.Errorf("Down = %v", got)
	}
}
