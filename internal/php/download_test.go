package php

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// serve publishes content as this machine's FrankenPHP build, with the given expected hash.
func serve(t *testing.T, name string, content []byte, sum string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		w.Write(content)
	}))
	t.Cleanup(srv.Close)
	key := runtime.GOOS + "/" + runtime.GOARCH
	prevBase := downloadBase
	prev, had := frankenPHPAssets[key]
	t.Cleanup(func() {
		downloadBase = prevBase
		if had {
			frankenPHPAssets[key] = prev
		} else {
			delete(frankenPHPAssets, key)
		}
	})
	downloadBase = srv.URL + "/"
	frankenPHPAssets[key] = asset{name, sum, int64(len(content))}
}

func hash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestInstallFrankenPHP(t *testing.T) {
	state := t.TempDir()
	content := []byte("#!/bin/sh\necho FrankenPHP\n")
	name := "frankenphp-test"
	if runtime.GOOS == "windows" {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, f := range []string{"frankenphp.exe", "php8ts.dll"} {
			w, _ := zw.Create(f)
			w.Write(content)
		}
		zw.Close()
		content, name = buf.Bytes(), "frankenphp-test.zip"
	}
	serve(t, name, content, hash(content))

	var got int64
	bin, err := InstallFrankenPHP(context.Background(), state, func(n int64) { got += n })
	if err != nil {
		t.Fatal(err)
	}
	if bin != FrankenPHPPath(state) || got != int64(len(content)) {
		t.Errorf("installed %s, %d bytes reported", bin, got)
	}
	info, err := os.Stat(bin)
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o755) {
		t.Errorf("binary: %v %v", info, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(filepath.Dir(bin)))
	if len(entries) != 1 {
		t.Errorf("left behind next to the version folder: %v", entries)
	}
	// Already there: nothing is downloaded again.
	got = 0
	if _, err := InstallFrankenPHP(context.Background(), state, func(n int64) { got += n }); err != nil || got != 0 {
		t.Errorf("second install: %v, %d bytes", err, got)
	}
}

func TestInstallFrankenPHPRefusesAMismatch(t *testing.T) {
	state := t.TempDir()
	serve(t, "frankenphp-test", []byte("tampered"), hash([]byte("the real build")))
	if _, err := InstallFrankenPHP(context.Background(), state, nil); err == nil || !strings.Contains(err.Error(), "does not match its release") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(FrankenPHPPath(state)); !os.IsNotExist(err) {
		t.Errorf("mismatching download kept: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(state, "frankenphp"))
	if len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestPinnedAssets(t *testing.T) {
	for key, a := range frankenPHPAssets {
		if len(a.sha256) != 64 || a.size < 1<<20 || !strings.HasPrefix(a.name, "frankenphp-") {
			t.Errorf("%s: %+v", key, a)
		}
	}
}
