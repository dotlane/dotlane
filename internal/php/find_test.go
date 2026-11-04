package php

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/dotlane/dotlane/internal/sites"
)

// fake writes a script at path that prints out, as php-fpm -v or frankenphp version would.
func fake(t *testing.T, path, out string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho '"+out+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fakes are shell scripts")
	}
	dir := t.TempDir()
	fake(t, filepath.Join(dir, "brew/opt/php@8.4/sbin/php-fpm"), "PHP 8.4.13 (fpm-fcgi) (built: Oct 1 2026)")
	fake(t, filepath.Join(dir, "brew/opt/php@8.5/sbin/php-fpm"), "PHP 8.5.12 (fpm-fcgi) (built: Oct 19 2026)")
	os.Symlink(filepath.Join(dir, "brew/opt/php@8.5"), filepath.Join(dir, "brew/opt/php")) // the same binary twice
	fake(t, filepath.Join(dir, "mise/php/8.3.20/sbin/php-fpm"), "PHP 8.3.20 (fpm-fcgi)")
	fake(t, filepath.Join(dir, "mise/php/8.4.14/sbin/php-fpm"), "PHP 8.4.14 (fpm-fcgi)")
	fake(t, filepath.Join(dir, "state/frankenphp/1.13.4/frankenphp"), "FrankenPHP v1.13.4 PHP 8.5.12 Caddy v2.11.8 h1:x")
	fake(t, filepath.Join(dir, "bin/php-fpm"), "PHP 8.2.0 (cli)") // not PHP-FPM
	os.WriteFile(filepath.Join(dir, "bin/frankenphp"), []byte("not executable"), 0o644)

	got := find(context.Background(), []place{
		{sites.RuntimeFrankenPHP, filepath.Join(dir, "state/frankenphp/*/frankenphp"), "Dotlane"},
		{sites.RuntimeFPM, filepath.Join(dir, "brew/opt/php*/sbin/php-fpm"), "Homebrew"},
		{sites.RuntimeFPM, filepath.Join(dir, "mise/php/*/sbin/php-fpm"), "mise"},
		{sites.RuntimeFPM, filepath.Join(dir, "bin/php-fpm"), "PATH"},
		{sites.RuntimeFrankenPHP, filepath.Join(dir, "bin/frankenphp"), "PATH"},
	})
	var names []string
	for _, i := range got {
		names = append(names, i.String())
	}
	want := "FrankenPHP 1.13.4 with PHP 8.5.12 (Dotlane), PHP 8.5.12 (Homebrew), PHP 8.4.14 (mise), PHP 8.4.13 (Homebrew), PHP 8.3.20 (mise)"
	if strings.Join(names, ", ") != want {
		t.Errorf("Find =\n%s\nwant\n%s", strings.Join(names, ", "), want)
	}
}

func TestChoose(t *testing.T) {
	installs := []Install{
		{Runtime: sites.RuntimeFrankenPHP, PHPVersion: "8.5.12", Version: "1.13.4", Binary: "/f2", Source: "Dotlane"},
		{Runtime: sites.RuntimeFPM, PHPVersion: "8.5.12", Binary: "/b85", Source: "Homebrew"},
		{Runtime: sites.RuntimeFrankenPHP, PHPVersion: "8.5.1", Version: "1.10.0", Binary: "/f1", Source: "PATH"},
		{Runtime: sites.RuntimeFPM, PHPVersion: "8.4.14", Binary: "/m84", Source: "mise"},
		{Runtime: sites.RuntimeFPM, PHPVersion: "8.4.13", Binary: "/b84", Source: "Homebrew"},
	}
	for _, tc := range []struct {
		php  sites.PHP
		want string
	}{
		{sites.PHP{Version: "8.4", Runtime: sites.RuntimeFPM}, "/m84"}, // newest 8.4
		{sites.PHP{Version: "latest", Runtime: sites.RuntimeFPM}, "/b85"},
		{sites.PHP{Version: "8.5", Runtime: sites.RuntimeFrankenPHP}, "/f2"}, // newest FrankenPHP
		{sites.PHP{Version: "latest", Runtime: sites.RuntimeFrankenPHP}, "/f2"},
	} {
		if got, err := Choose(installs, tc.php); err != nil || got.Binary != tc.want {
			t.Errorf("%+v: %v, %v; want %s", tc.php, got.Binary, err, tc.want)
		}
	}

	for _, tc := range []struct {
		installs []Install
		php      sites.PHP
		want     []string
	}{
		{installs, sites.PHP{Version: "8.3", Runtime: sites.RuntimeFPM}, []string{"PHP 8.3 with PHP-FPM is not installed", "found: PHP 8.5.12 (Homebrew), PHP 8.4.14 (mise)", "install it:"}},
		{installs, sites.PHP{Version: "8.4", Runtime: sites.RuntimeFrankenPHP}, []string{"no FrankenPHP here comes with PHP 8.4", `php = "8.5"`, `runtime = "fpm"`}},
		{installs[1:2], sites.PHP{Version: "8.5", Runtime: sites.RuntimeFrankenPHP}, []string{"FrankenPHP is not installed", "dotlane frankenphp install"}},
		{nil, sites.PHP{Version: "latest", Runtime: sites.RuntimeFPM}, []string{"no PHP with PHP-FPM is installed"}},
	} {
		_, err := Choose(tc.installs, tc.php)
		var nf *NotFoundError
		if !errors.As(err, &nf) {
			t.Errorf("%+v: %v, want NotFoundError", tc.php, err)
			continue
		}
		for _, w := range tc.want {
			if runtime.GOOS == "windows" && tc.php.Runtime == sites.RuntimeFPM {
				w = "PHP-FPM does not exist on Windows"
			}
			if !strings.Contains(err.Error(), w) {
				t.Errorf("%+v: %q lacks %q", tc.php, err, w)
			}
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		sign int
	}{{"8.4.13", "8.4.9", 1}, {"8.10.0", "8.9.0", 1}, {"1.13.4", "1.13.4", 0}, {"8.4", "8.4.1", -1}} {
		got := compareVersions(tc.a, tc.b)
		if (got > 0) != (tc.sign > 0) || (got < 0) != (tc.sign < 0) {
			t.Errorf("compare(%s, %s) = %d", tc.a, tc.b, got)
		}
	}
}
