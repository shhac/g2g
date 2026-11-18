package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/landed"
	"github.com/shhac/g2g/internal/testutil"
)

// absorbedSource is the digest of the file that decides absorption, as it was
// when absorbedRules was last set.
const absorbedSource = "cdcc0928604113b21a465e5cd38c07e2609bfa16c91c8cf283dc401e49c77006"

// A remembered answer is only as right as the rule that made it, and it is
// kept across releases. Changing how absorption is decided has to throw the
// old answers away, which bumping absorbedRules does; this fails whenever
// that file changes, so the question is asked every time. If the change does
// not alter what is decided, update the digest alone.
func TestRememberedVerdictsAreTiedToTheRuleThatMadeThem(t *testing.T) {
	source, err := os.ReadFile("content.go")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	if got := hex.EncodeToString(digest[:]); got != absorbedSource {
		t.Fatalf("content.go changed (digest %s) · if what counts as absorbed changed, bump absorbedRules (now %d); then set absorbedSource to that digest", got, absorbedRules)
	}
}

// buried is a branch squashed into a trunk that then edited over it, under
// enough later edits that finding the squash costs a merge per candidate.
func buried(t *testing.T, landed bool) testutil.GitRepo {
	t.Helper()
	body := "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n"
	repo := testutil.NewGitRepo(t, "synthetic-trunk")
	repo.Commit("synthetic base", "work.txt", body)
	repo.Run("checkout", "-q", "-b", "synthetic-a")
	repo.Commit("synthetic mine", "work.txt", strings.Replace(body, "two", "TWO", 1))
	repo.Run("checkout", "-q", "synthetic-trunk")
	if landed {
		repo.Run("merge", "-q", "--squash", "synthetic-a")
		repo.Run("commit", "-qm", "synthetic squash")
	}
	for i := range 6 {
		repo.Commit(fmt.Sprintf("synthetic busy %d", i), "work.txt", strings.Replace(body, "two", fmt.Sprintf("two %d", i), 1))
	}
	inRepo(t, repo.Dir)
	return repo
}

func merges(t *testing.T, ctx context.Context, want bool) int {
	t.Helper()
	runner := &countingRunner{subcommand: "merge-tree"}
	absorbed, err := Client{Runner: runner}.Absorbed(ctx, "synthetic-trunk", "synthetic-a")
	if err != nil || absorbed != want {
		t.Fatalf("Absorbed() = %v, %v, want %v", absorbed, err, want)
	}
	return runner.calls
}

// A branch found landed under later edits is found again for the price of
// the merge at the tip, for as long as the trunk still holds the commit that
// took it in -- and asked afresh when the answer is to justify a deletion.
func TestALandedBranchIsNotSearchedForTwice(t *testing.T) {
	repo := buried(t, true)
	ctx := context.Background()
	if first := merges(t, ctx, true); first < 3 {
		t.Fatalf("the first look made %d merges · the squash is expected to be buried", first)
	}
	if again := merges(t, ctx, true); again != 1 {
		t.Errorf("the second look made %d merges, want only the one at the tip", again)
	}
	repo.Commit("synthetic later", "work.txt", "one\ntwo later\nthree\nfour\nfive\nsix\nseven\neight\n")
	if later := merges(t, ctx, true); later != 1 {
		t.Errorf("a look after the trunk moved on made %d merges, want only the one at the tip", later)
	}
	if fresh := merges(t, landed.Fresh(ctx), true); fresh < 3 {
		t.Errorf("a fresh look made %d merges, want the whole search", fresh)
	}
}

// A branch that has not landed is asked again only about what the trunk
// gained since it was last asked.
func TestABranchThatHasNotLandedIsAskedOnlyAboutWhatIsNew(t *testing.T) {
	repo := buried(t, false)
	ctx := context.Background()
	if first := merges(t, ctx, false); first != 7 {
		t.Fatalf("the first look made %d merges, want the tip and each of the six edits", first)
	}
	if again := merges(t, ctx, false); again != 1 {
		t.Errorf("a second look at the same trunk made %d merges, want only the one at the tip", again)
	}
	repo.Commit("synthetic later", "work.txt", "one\ntwo later\nthree\nfour\nfive\nsix\nseven\neight\n")
	if later := merges(t, ctx, false); later != 2 {
		t.Errorf("a look after one more edit made %d merges, want the tip and that edit", later)
	}

	// And it lands after all: the remembered "no" does not hide it.
	repo.Run("checkout", "-q", "-b", "synthetic-squashed", "synthetic-trunk~1")
	repo.Run("checkout", "-q", "synthetic-trunk")
	repo.Run("reset", "-q", "--hard", "HEAD~7")
	repo.Run("merge", "-q", "--squash", "synthetic-a")
	repo.Run("commit", "-qm", "synthetic squash")
	repo.Commit("synthetic follow-up", "work.txt", "one\nTWO!\nthree\nfour\nfive\nsix\nseven\neight\n")
	merges(t, ctx, true)
}

// The branch moving is another commit, and the answer for the old one says
// nothing about it. A trunk that was rewritten no longer holds the commit an
// answer rested on.
func TestARememberedAnswerIsForOneCommitAndOneHistory(t *testing.T) {
	repo := buried(t, true)
	ctx := context.Background()
	merges(t, ctx, true)
	repo.Run("checkout", "-q", "synthetic-a")
	repo.Commit("synthetic more", "more.txt", "more")
	repo.Run("checkout", "-q", "synthetic-trunk")
	merges(t, ctx, false)

	repo.Run("checkout", "-q", "synthetic-a")
	repo.Run("reset", "-q", "--hard", "HEAD~1")
	repo.Run("checkout", "-q", "synthetic-trunk")
	merges(t, ctx, true)
	repo.Run("reset", "-q", "--hard", "HEAD~7")
	repo.Commit("synthetic rewritten", "work.txt", "one\ntwo rewritten\nthree\nfour\nfive\nsix\nseven\neight\n")
	merges(t, ctx, false)
}

// A file that cannot be read, or was made under another rule, holds nothing,
// and a look the deadline cut off is not remembered as an answer.
func TestOnlyAnAnswerIsRemembered(t *testing.T) {
	repo := buried(t, true)
	file := filepath.Join(repo.Dir, ".git", "g2g", "landed.json")
	ctx := context.Background()
	for name, content := range map[string]string{"unreadable": "synthetic nonsense", "another rule": `{"rules":0,"verdicts":{}}`} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if made := merges(t, ctx, true); made < 3 {
			t.Errorf("%s: %d merges, want the whole search", name, made)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, _ = Client{Runner: &countingRunner{subcommand: "merge-tree"}}.Absorbed(cancelled, "synthetic-trunk", "synthetic-a")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("a look that was cut off left an answer behind: %v", err)
	}
}
