package install

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dotlane/dotlane/internal/config"
)

// One command at a time holds the lock; the next waits for it, and gives up after its wait or
// when its context ends.
func TestLockService(t *testing.T) {
	cfg := &config.Config{StateDir: filepath.Join(t.TempDir(), "state")}
	ctx := context.Background()
	unlock, err := lockService(ctx, cfg, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := lockService(ctx, cfg, 200*time.Millisecond); err == nil {
		t.Fatal("took a lock that another command holds")
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err := lockService(short, cfg, time.Minute); err == nil {
		t.Fatal("kept waiting after the context ended")
	}

	got := make(chan error)
	go func() {
		unlock, err := lockService(ctx, cfg, 5*time.Second)
		if err == nil {
			unlock()
		}
		got <- err
	}()
	time.Sleep(150 * time.Millisecond)
	unlock()
	if err := <-got; err != nil {
		t.Errorf("waiting command: %v", err)
	}
}
