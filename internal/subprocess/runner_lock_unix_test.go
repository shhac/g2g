//go:build unix

package subprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/testutil"
)

// update-index holds a real Git lock while it asks fsmonitor to refresh the
// index. Cancellation must let Git's signal handler remove that lock; killing
// it outright leaves every subsequent writer blocked by a stale index.lock.
func TestCanceledGitReleasesItsIndexLock(t *testing.T) {
	repo := testutil.NewGitRepo(t, "synthetic-main")
	repo.Commit("synthetic root", "file.txt", "synthetic")
	t.Chdir(repo.Dir)
	marker := filepath.Join(repo.Dir, "monitor-started")
	hook := filepath.Join(repo.Dir, "monitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch monitor-started\nsleep 5\nprintf 'synthetic-token\\0'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (ExecRunner{}).Run(ctx, "git", "-c", "core.fsmonitor="+hook, "update-index", "--refresh")
		done <- err
	}()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("git exited before holding its lock: %v", err)
		case <-deadline:
			t.Fatal("git never reached fsmonitor")
		case <-tick.C:
		}
	}
	lock := filepath.Join(repo.Dir, ".git", "index.lock")
	if _, err := os.Stat(lock); err != nil {
		t.Fatalf("git is not holding its index lock: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() = %v, want cancellation", err)
		}
	case <-deadline:
		t.Fatal("cancellation did not stop git")
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled git left its index lock behind: %v", err)
	}
	repo.Run("update-index", "--refresh")
}

func TestCancellationForcesAnUnresponsiveProcessGroupToStop(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	t.Setenv("SYNTHETIC_SURVIVOR", marker)
	testutil.WithFakeExecutables(t, map[string]string{
		"gt": "trap '' TERM\n(sleep 2; echo survived > \"$SYNTHETIC_SURVIVOR\") &\nwait\n",
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := (ExecRunner{}).Run(ctx, "gt")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 10*time.Second {
		t.Fatalf("unresponsive group was not stopped promptly: %v", err)
	}
	// Beyond the child's planned write, so a surviving descendant cannot pass
	// merely because it had not reached its write when Run returned.
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a descendant survived cancellation: %v", err)
	}
}
