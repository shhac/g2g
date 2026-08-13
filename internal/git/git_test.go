package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/gt2gh/internal/subprocess"
	"github.com/shhac/gt2gh/internal/testutil"
)

func TestPushAtomicUsesOneLeaseProtectedInvocation(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
if [ "$1 $2" = "remote get-url" ]; then printf 'https://example.test/synthetic.git\n'; exit 0; fi
if [ "$1" = "push" ]; then exit 0; fi
exit 9`,
	})
	if err := (Client{Runner: subprocess.ExecRunner{}}).PushAtomic(context.Background(), "origin", []string{"synthetic-lower", "synthetic-top"}); err != nil {
		t.Fatal(err)
	}
	called, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(called), "remote get-url origin\npush --atomic --force-with-lease origin synthetic-lower synthetic-top\n"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

func TestPushAtomicDoesNotFallbackOnAtomicOrLeaseFailure(t *testing.T) {
	for _, failure := range []string{"atomic unsupported", "stale info"} {
		t.Run(failure, func(t *testing.T) {
			arguments := filepath.Join(t.TempDir(), "git-arguments")
			t.Setenv("GIT_ARGUMENTS", arguments)
			testutil.WithFakeExecutables(t, map[string]string{
				"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
if [ "$1 $2" = "remote get-url" ]; then exit 0; fi
printf '%s\n' 'synthetic push failure' >&2
exit 1`,
			})
			err := (Client{Runner: subprocess.ExecRunner{}}).PushAtomic(context.Background(), "origin", []string{"synthetic-branch"})
			if err == nil {
				t.Fatal("PushAtomic() error = nil")
			}
			called, readErr := os.ReadFile(arguments)
			if readErr != nil || strings.Count(string(called), "push ") != 1 || !strings.Contains(string(called), "push --atomic --force-with-lease origin synthetic-branch") {
				t.Errorf("calls=%q readErr=%v", called, readErr)
			}
		})
	}
}

func TestRemoteRejectsUnsafeName(t *testing.T) {
	if err := (Client{}).Remote(context.Background(), "-synthetic"); err == nil {
		t.Fatal("Remote() error = nil")
	}
}

func TestPushAtomicRejectsEmptyBranchList(t *testing.T) {
	arguments := filepath.Join(t.TempDir(), "git-arguments")
	t.Setenv("GIT_ARGUMENTS", arguments)
	testutil.WithFakeExecutables(t, map[string]string{
		"git": `printf '%s\n' "$*" >> "$GIT_ARGUMENTS"
if [ "$1 $2" = "remote get-url" ]; then exit 0; fi
exit 9`,
	})
	err := (Client{Runner: subprocess.ExecRunner{}}).PushAtomic(context.Background(), "origin", nil)
	if err == nil || !strings.Contains(err.Error(), "no branches") {
		t.Fatalf("PushAtomic() error = %v", err)
	}
	called, readErr := os.ReadFile(arguments)
	if readErr != nil || strings.Contains(string(called), "push ") {
		t.Errorf("calls=%q readErr=%v", called, readErr)
	}
}
