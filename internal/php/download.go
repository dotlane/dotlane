package php

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FrankenPHPVersion is the FrankenPHP release dotlane frankenphp install downloads. Each Dotlane
// release pins one, with every file's SHA-256 as GitHub lists it for that release, and a
// download that does not match is never run.
const FrankenPHPVersion = "1.13.4"

// asset is a FrankenPHP release file.
type asset struct {
	name, sha256 string
	size         int64
}

// frankenPHPAssets are FrankenPHPVersion's builds, by GOOS/GOARCH. There is none for Intel Macs.
var frankenPHPAssets = map[string]asset{
	"darwin/arm64":  {"frankenphp-mac-arm64", "fa9cd55ea362f3521e9f8a40308bf866e525cdf3b33f5c6c111ebfc7af94e4b4", 199276776},
	"linux/amd64":   {"frankenphp-linux-x86_64", "7397e224700ec8ff299c9a6433e1ae7e2f15ffb15df80fc4535f0f1512760274", 175418720},
	"linux/arm64":   {"frankenphp-linux-aarch64", "d0b403456083d30887d7d73c46fbc49aba0244d246dd4973a54a6c7b6669e3c4", 171453448},
	"windows/amd64": {"frankenphp-windows-x86_64.zip", "1e340f3fcf4db5115dc511cf38441ca5d411b9c1dd2b614eeada1c06595f3229", 60433045},
}

// downloadBase is where the release files are; tests point it at a local server.
var downloadBase = "https://github.com/php/frankenphp/releases/download/v" + FrankenPHPVersion + "/"

// FrankenPHPDownload describes the build for this machine: its file name and size, or an error
// when FrankenPHP publishes none.
func FrankenPHPDownload() (name string, size int64, err error) {
	a, ok := frankenPHPAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", 0, fmt.Errorf("FrankenPHP publishes no build for %s/%s\n"+
			"build it yourself (https://frankenphp.dev/docs/compile/) and put frankenphp on your PATH, or use PHP-FPM",
			runtime.GOOS, runtime.GOARCH)
	}
	return a.name, a.size, nil
}

// FrankenPHPPath is where InstallFrankenPHP puts the binary.
func FrankenPHPPath(stateDir string) string {
	exe := "frankenphp"
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	return filepath.Join(stateDir, "frankenphp", FrankenPHPVersion, exe)
}

// InstallFrankenPHP downloads FrankenPHPVersion's build for this machine into the state
// directory, checks its SHA-256 and returns the binary's path. progress, if not nil, is told how
// many bytes have arrived. An existing, complete download is kept.
func InstallFrankenPHP(ctx context.Context, stateDir string, progress func(done int64)) (string, error) {
	a, ok := frankenPHPAssets[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		_, _, err := FrankenPHPDownload()
		return "", err
	}
	bin := FrankenPHPPath(stateDir)
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	// Built in a folder of its own, renamed into place only once complete.
	parent := filepath.Dir(filepath.Dir(bin))
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(parent, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	tmp, err := os.CreateTemp(staging, a.name+"-*")
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if err := fetch(ctx, downloadBase+a.name, tmp, a, progress); err != nil {
		return "", err
	}
	if strings.HasSuffix(a.name, ".zip") { // Windows: frankenphp.exe and PHP's DLLs
		if err := unzip(tmp, staging); err != nil {
			return "", err
		}
		if _, err := os.Stat(filepath.Join(staging, filepath.Base(bin))); err != nil {
			return "", fmt.Errorf("%s has no %s", a.name, filepath.Base(bin))
		}
	} else if err := errors.Join(tmp.Chmod(0o755), os.Rename(tmp.Name(), filepath.Join(staging, filepath.Base(bin)))); err != nil {
		return "", err
	}
	tmp.Close()
	os.Remove(tmp.Name())           // the archive, once extracted
	os.RemoveAll(filepath.Dir(bin)) // an incomplete folder from before
	return bin, os.Rename(staging, filepath.Dir(bin))
}

// fetch downloads url into f, checking its size and SHA-256 against a.
func fetch(ctx context.Context, url string, f *os.File, a asset, progress func(int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading FrankenPHP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading FrankenPHP: %s answered %s", url, resp.Status)
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	if progress != nil {
		w = io.MultiWriter(w, progressWriter(progress))
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, a.size+1))
	if err != nil {
		return fmt.Errorf("downloading FrankenPHP: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); n != a.size || got != a.sha256 {
		return fmt.Errorf("the FrankenPHP download does not match its release (%d bytes, SHA-256 %s; expected %d bytes, %s), so it was deleted",
			n, got, a.size, a.sha256)
	}
	return nil
}

type progressWriter func(int64)

func (p progressWriter) Write(b []byte) (int, error) {
	p(int64(len(b)))
	return len(b), nil
}

// unzip extracts the archive f into dir; a name that would land outside dir is refused.
func unzip(f *os.File, dir string) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	r, err := zip.NewReader(f, info.Size())
	if err != nil {
		return err
	}
	for _, zf := range r.File {
		if !filepath.IsLocal(zf.Name) {
			return fmt.Errorf("the FrankenPHP archive has a file outside its folder: %s", zf.Name)
		}
		path := filepath.Join(dir, zf.Name)
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := extract(zf, path); err != nil {
			return err
		}
	}
	return nil
}

func extract(zf *zip.File, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	in, err := zf.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return errors.Join(err, out.Close())
}
