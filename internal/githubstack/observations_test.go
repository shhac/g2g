package githubstack

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shhac/g2g/internal/diagnostic"
	"github.com/shhac/g2g/internal/subprocess"
	"github.com/shhac/g2g/internal/testutil"
)

type observationLocator string

func (p observationLocator) CommonDir(context.Context) (string, error) { return string(p), nil }

func TestPRObservationsRememberReplacementAndConfirmedMerge(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &FileObservations{Git: observationLocator(t.TempDir()), Now: func() time.Time { return now }}
	old := PullRequest{Number: 41, URL: "https://example.test/synthetic/repo/pull/41", Head: "synthetic-work", HeadOID: "synthetic-tip", Base: "synthetic-main", State: "CLOSED"}
	current := old
	current.Number, current.URL, current.State = 42, "https://example.test/synthetic/repo/pull/42", "OPEN"
	if err := store.Remember(ctx, []PullRequest{old, current}); err != nil {
		t.Fatal(err)
	}
	if err := store.MergeRequested(ctx, 42, "synthetic-tip"); err != nil {
		t.Fatal(err)
	}
	seen, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := seen[current.Head]; got.PullRequest.State != "OPEN" || got.MergeRequestedAt.IsZero() {
		t.Fatalf("merge request became a merge: %+v", got)
	}
	now = now.Add(time.Minute)
	if err := store.ObserveStates(ctx, map[int]MergeState{42: {Number: 42, Head: current.Head, HeadOID: current.HeadOID, Base: current.Base, State: "MERGED"}}); err != nil {
		t.Fatal(err)
	}
	seen, err = (&FileObservations{Git: store.Git}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := seen[current.Head]; got.PullRequest.Number != 42 || got.PullRequest.State != "MERGED" || !got.MergeRequestedAt.IsZero() || !got.ObservedAt.Equal(now) {
		t.Fatalf("confirmed merge = %+v", got)
	}
	// Reusing the branch name opens a new observation, not a merged new PR.
	current.Number, current.URL = 43, "https://example.test/synthetic/repo/pull/43"
	if err := store.Remember(ctx, []PullRequest{current}); err != nil {
		t.Fatal(err)
	}
	seen, _ = store.Load(ctx)
	if got := seen[current.Head]; got.PullRequest.Number != 43 || got.PullRequest.State != "OPEN" || !got.MergeRequestedAt.IsZero() {
		t.Fatalf("replacement = %+v", got)
	}
}

func TestPRObservationWritesArePrivateAndConcurrentSafe(t *testing.T) {
	store := &FileObservations{Git: observationLocator(t.TempDir())}
	var wg sync.WaitGroup
	for _, branch := range []string{"synthetic-a", "synthetic-b", "synthetic-c"} {
		wg.Add(1)
		go func(branch string) {
			defer wg.Done()
			if err := store.Remember(context.Background(), []PullRequest{{Head: branch, URL: "https://example.test/" + branch, Number: 1, State: "OPEN"}}); err != nil {
				t.Error(err)
			}
		}(branch)
	}
	wg.Wait()
	seen, err := store.Load(context.Background())
	if err != nil || len(seen) != 3 {
		t.Fatalf("observations = %+v, %v", seen, err)
	}
	path, _ := store.path(context.Background())
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("cache permissions: %v, %v", info, err)
	}
}

func TestPRObservationUnreadableSchemaIsReported(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "g2g"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "g2g", "pull-requests.json"), []byte(`{"schemaVersion":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&FileObservations{Git: observationLocator(dir)}).Load(context.Background()); err == nil {
		t.Fatal("accepted a future schema")
	}
	// A PR creation can succeed even when the local cache is unreadable. It
	// must warn and keep the future document, rather than fail the action or
	// replace data written by a newer client.
	testutil.FakeCLIs(t, map[string][]testutil.Route{"gh": {{Prefix: "pr create", Output: "https://example.test/synthetic/repo/pull/41"}}})
	var warnings bytes.Buffer
	ctx := diagnostic.WithWarningWriter(context.Background(), &warnings)
	client := Client{Runner: subprocess.ExecRunner{}, Observations: &FileObservations{Git: observationLocator(dir)}}
	if err := client.Create(ctx, "synthetic-work", "synthetic-main", "Synthetic change", "Synthetic body", true, nil); err != nil {
		t.Fatalf("local cache failure failed successful PR creation: %v", err)
	}
	if !strings.Contains(warnings.String(), "could not remember PR state locally") {
		t.Fatalf("no cache warning: %s", warnings.String())
	}
	data, err := os.ReadFile(filepath.Join(dir, "g2g", "pull-requests.json"))
	if err != nil || string(data) != `{"schemaVersion":99}` {
		t.Fatalf("overwrote a future cache: %s, %v", data, err)
	}
}

func TestPRCreationRemembersItsIdentityWithoutAnotherQuery(t *testing.T) {
	store := &FileObservations{Git: observationLocator(t.TempDir())}
	recorder := testutil.FakeCLIs(t, map[string][]testutil.Route{"gh": {{Prefix: "pr create", Lines: []string{"synthetic notice", "https://example.test/synthetic/repo/pull/41"}}}})
	client := Client{Runner: subprocess.ExecRunner{}, Observations: store}
	if err := client.Create(context.Background(), "synthetic-work", "synthetic-main", "Synthetic change", "Synthetic body", true, nil); err != nil {
		t.Fatal(err)
	}
	seen, err := store.Load(context.Background())
	if err != nil || seen["synthetic-work"].PullRequest.Number != 41 {
		t.Fatalf("created observation = %+v, %v", seen, err)
	}
	recorder.Find("gh pr create --head synthetic-work --base synthetic-main")
	recorder.AssertNone("gh api ")
}

func TestFailedRemoteMutationDoesNotChangeRememberedState(t *testing.T) {
	store := &FileObservations{Git: observationLocator(t.TempDir())}
	pr := PullRequest{Head: "synthetic-work", HeadOID: "synthetic-tip", Number: 41, URL: "https://example.test/synthetic/repo/pull/41", State: "OPEN"}
	if err := store.Remember(context.Background(), []PullRequest{pr}); err != nil {
		t.Fatal(err)
	}
	testutil.FakeCLIs(t, map[string][]testutil.Route{"gh": {{Prefix: "pr merge", Stderr: "synthetic merge refusal", Exit: 1}}})
	client := Client{Runner: subprocess.ExecRunner{}, Observations: store}
	if err := client.Merge(context.Background(), 41, MethodSquash, false, "synthetic-tip"); err == nil {
		t.Fatal("expected a refusal")
	}
	seen, _ := store.Load(context.Background())
	if seen[pr.Head].PullRequest.State != "OPEN" || !seen[pr.Head].MergeRequestedAt.IsZero() {
		t.Fatalf("failed merge changed cache: %+v", seen)
	}
}

func TestCreatedPRParsingRejectsNonPRURLs(t *testing.T) {
	for _, output := range []string{"", "synthetic warning", "https://example.test/synthetic/repo", "https://example.test/synthetic/repo/pull/no-number", "file:///synthetic/repo/pull/41"} {
		if _, ok := createdPullRequest([]byte(output), "synthetic-work", "synthetic-main"); ok {
			t.Errorf("accepted %q", output)
		}
	}
	if _, ok := createdPullRequest([]byte(strings.Join([]string{"synthetic notice", "https://example.test/synthetic/repo/pull/41"}, "\n")), "synthetic-work", "synthetic-main"); !ok {
		t.Fatal("missed PR URL after a notice")
	}
}
