package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/testutil"
)

// These drive whole restack scenarios against real Git, from the simplest
// stack to the shapes that have historically broken.
//
// Ancestry and rewriting are the one seam a PATH fake proves nothing about: a
// fake answers whatever it is asked, and the question is what Git actually
// considers reachable and actually produces. So each scenario builds a
// throwaway repository with synthetic names and no remote.
//
// The value is in the invariants rather than the individual assertions. Every
// scenario checks the same five things, so a shape nobody thought to assert
// about is still covered the moment it is added to the table.

// commit is one commit on one branch. Branches are created in order, each from
// the parent named, so the table reads as the shape it builds.
type commit struct {
	branch  string
	parent  string
	file    string
	content string
}

// scenario is a repository shape plus what happens to its trunk afterwards.
type scenario struct {
	name string
	// commits build the stack, in order.
	commits []commit
	// merged names a branch whose work is squash-merged into the trunk after
	// the stack is recorded, which is what a landed pull request looks like.
	merged string
	// rewrite makes the trunk rewrite the same lines the stack touches, so the
	// replay cannot apply cleanly and the resumable engine is used.
	rewrite bool
	// dropped names a branch whose commits the user removed by hand before the
	// restack, which must stay removed.
	dropped string
	// bottomUp records the stack one branch at a time from the tip downwards
	// instead of in one step. That is the order the candidate list leads a user
	// into, and it is where the trunk set used to drift: every branch passed
	// through being a root on the way and none of them stopped being one.
	bottomUp bool
}

// repo is the shared throwaway repository plus the few things only these
// scenarios ask of it.
type repo struct {
	t    *testing.T
	dir  string
	repo testutil.GitRepo
}

func (r repo) git(args ...string) string { return r.repo.Run(args...) }

func (r repo) write(name, content string) { r.repo.Write(name, content) }

// build creates the repository the scenario describes and leaves the checkout
// on the last branch named.
func (s scenario) build(t *testing.T) repo {
	t.Helper()

	built := testutil.NewGitRepo(t, "synthetic-trunk")
	r := repo{t: t, dir: built.Dir, repo: built}
	r.write("shared.txt", "base\n")
	r.git("add", "-A")
	r.git("commit", "-qm", "base")

	created := map[string]bool{"synthetic-trunk": true}
	for _, c := range s.commits {
		if !created[c.branch] {
			r.git("checkout", "-q", c.parent)
			r.git("checkout", "-qb", c.branch)
			created[c.branch] = true
		} else {
			r.git("checkout", "-q", c.branch)
		}
		r.write(c.file, c.content)
		r.git("add", "-A")
		r.git("commit", "-qm", c.branch+"/"+c.file)
	}
	t.Chdir(r.dir)
	return r
}

// recordedGraph reads the store the way another command would.
func (r repo) recordedGraph() struct {
	Trunks   []string                     `json:"trunks"`
	Branches map[string]map[string]string `json:"branches"`
} {
	r.t.Helper()
	var recorded struct {
		Trunks   []string                     `json:"trunks"`
		Branches map[string]map[string]string `json:"branches"`
	}
	stored, err := os.ReadFile(filepath.Join(r.dir, ".git", "g2g", "graph.json"))
	if err != nil {
		r.t.Fatalf("read graph: %v", err)
	}
	if err := json.Unmarshal(stored, &recorded); err != nil {
		r.t.Fatal(err)
	}
	return recorded
}

// scenarios run from the simplest useful stack to the shapes that have broken
// before. Adding a row is the cheapest way to cover a new one.
var scenarios = []scenario{
	{
		name: "one branch on the trunk",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
		},
	},
	{
		name: "two-branch chain",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
		},
	},
	{
		name: "four-branch chain",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
			{branch: "synthetic-d", parent: "synthetic-c", file: "d.txt", content: "d\n"},
		},
	},
	{
		name: "branch with several commits",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a1\n"},
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a2.txt", content: "a2\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
		},
	},
	{
		name: "fork: two branches on one parent",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-a", file: "c.txt", content: "c\n"},
		},
	},
	{
		name: "deep fork: a tree three levels down",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
			{branch: "synthetic-d", parent: "synthetic-b", file: "d.txt", content: "d\n"},
			{branch: "synthetic-e", parent: "synthetic-a", file: "e.txt", content: "e\n"},
		},
	},
	{
		name: "chain whose base has landed",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
		},
		merged: "synthetic-a",
	},
	{
		name: "tree whose base has landed",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-a", file: "c.txt", content: "c\n"},
		},
		merged: "synthetic-a",
	},
	{
		name: "landed base and a conflicting rewrite",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "shared.txt", content: "base\na\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "shared.txt", content: "base\na\nb\n"},
		},
		merged:  "synthetic-a",
		rewrite: true,
	},
	{
		name: "three deep, landed base, conflict mid-chain",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "shared.txt", content: "base\na\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "shared.txt", content: "base\na\nb\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
		},
		merged:  "synthetic-a",
		rewrite: true,
	},
	{
		name: "four deep, landed base, conflict mid-chain",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "shared.txt", content: "base\na\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "shared.txt", content: "base\na\nb\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
			{branch: "synthetic-d", parent: "synthetic-c", file: "d.txt", content: "d\n"},
		},
		merged:  "synthetic-a",
		rewrite: true,
	},
	{
		name: "chain recorded one branch at a time from the tip",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
		},
		bottomUp: true,
	},
	{
		name: "landed base, recorded one branch at a time from the tip",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
		},
		merged:   "synthetic-a",
		bottomUp: true,
	},
	{
		name: "two independent roots",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b.txt", content: "b\n"},
			{branch: "synthetic-x", parent: "synthetic-trunk", file: "x.txt", content: "x\n"},
			{branch: "synthetic-y", parent: "synthetic-x", file: "y.txt", content: "y\n"},
		},
	},
	{
		name: "commit removed by hand stays removed",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b1.txt", content: "b1\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b2.txt", content: "b2\n"},
		},
		dropped: "synthetic-b",
	},
	{
		name: "landed base and a commit removed by hand",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "a.txt", content: "a\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b1.txt", content: "b1\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "b2.txt", content: "b2\n"},
		},
		merged:  "synthetic-a",
		dropped: "synthetic-b",
	},
	{
		name: "five deep, landed base, conflict at the bottom",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "shared.txt", content: "base\na\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "shared.txt", content: "base\na\nb\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
			{branch: "synthetic-d", parent: "synthetic-c", file: "d.txt", content: "d\n"},
			{branch: "synthetic-e", parent: "synthetic-d", file: "e.txt", content: "e\n"},
		},
		merged:  "synthetic-a",
		rewrite: true,
	},
	{
		name: "fork above a conflicting rewrite",
		commits: []commit{
			{branch: "synthetic-a", parent: "synthetic-trunk", file: "shared.txt", content: "base\na\n"},
			{branch: "synthetic-b", parent: "synthetic-a", file: "shared.txt", content: "base\na\nb\n"},
			{branch: "synthetic-c", parent: "synthetic-b", file: "c.txt", content: "c\n"},
			{branch: "synthetic-d", parent: "synthetic-b", file: "d.txt", content: "d\n"},
		},
		merged:  "synthetic-a",
		rewrite: true,
	},
}

// TestRestackScenarios drives every shape through record → land → restack and
// checks the same invariants for each.
func TestRestackScenarios(t *testing.T) {
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			r := s.build(t)
			tips := r.branchTips()

			s.record(t, r)
			s.land(r)
			s.drop(r)

			r.restackToCompletion(t)
			r.assertInvariants(t, s, tips)
		})
	}
}

// record adopts the stack, either in one step or the way a user working from
// the candidate list would: one branch at a time, from the tip downwards.
func (s scenario) record(t *testing.T, r repo) {
	t.Helper()

	if !s.bottomUp {
		if _, _, err := run(t, "track", "--stack", "--trunk", "synthetic-trunk", "--apply"); err != nil {
			t.Fatalf("track --stack: %v", err)
		}
		return
	}
	for index := len(s.commits) - 1; index >= 0; index-- {
		c := s.commits[index]
		if _, _, err := run(t, "track", "--branch", c.branch, "--parent", c.parent, "--apply"); err != nil {
			t.Fatalf("track %s: %v", c.branch, err)
		}
	}
}

// droppedSubject names the one commit drop removes: a branch's most recent.
func (s scenario) droppedSubject() string {
	if s.dropped == "" {
		return ""
	}
	subject := ""
	for _, c := range s.commits {
		if c.branch == s.dropped {
			subject = c.branch + "/" + c.file
		}
	}
	return subject
}

// drop removes a branch's most recent commit by hand, the way someone who decided a
// change was unwanted would. A restack must leave them removed rather than
// replaying them back in.
func (s scenario) drop(r repo) {
	if s.dropped == "" {
		return
	}
	r.git("checkout", "-q", s.dropped)
	r.git("reset", "--hard", "-q", "HEAD~1")
	r.git("checkout", "-q", s.commits[len(s.commits)-1].branch)
}

// land squash-merges the named branch into the trunk, and optionally rewrites
// the lines the stack touches so the replay cannot apply cleanly.
func (s scenario) land(r repo) {
	if s.merged == "" {
		return
	}
	r.git("checkout", "-q", "synthetic-trunk")
	r.git("merge", "--squash", "-q", s.merged)
	r.git("commit", "-qm", s.merged+" (#1)")
	if s.rewrite {
		r.write("shared.txt", "base\nrewritten-upstream\n")
		r.git("add", "-A")
		r.git("commit", "-qm", "trunk rewrites the same lines")
	}
	r.git("checkout", "-q", s.commits[len(s.commits)-1].branch)
}

// restackToCompletion repairs the whole stack the way a user would, and is the
// only place that knows how.
//
// A forking selection whose rewrite conflicts is refused on purpose: the
// resumable engine moves one line of descent per invocation, so it names the
// remedy rather than guessing which line to take. Following that remedy is part
// of what these scenarios check — a refusal whose instructions do not work is
// no better than a wrong answer.
func (r repo) restackToCompletion(t *testing.T) {
	t.Helper()

	// Selected from the trunk, "my stack" is the whole tree: a trunk's path is
	// itself, so the scope reduces to everything under it. That is how these
	// scenarios ask for the entire shape without a scope that reaches other
	// trunks, which is exactly what a rewrite must not be handed.
	stdout, _, err := run(t, "restack", "--branch", "synthetic-trunk", "--scope", "stack", "--apply")
	if err == nil {
		r.settle(t, stdout)
		return
	}
	if !strings.Contains(err.Error(), "forks and the rewrite conflicts") {
		t.Fatalf("restack --apply: %v\n%s", err, stdout)
	}
	for _, leaf := range r.leaves(t) {
		// A line already correct refuses with nothing to do, which is not a
		// failure of the sequence.
		out, _, lineErr := run(t, "restack", "--branch", leaf, "--scope", "path", "--apply")
		if lineErr != nil {
			continue
		}
		r.settle(t, out)
	}
}

// settle answers each conflict the way a user would until the command stops
// asking. It refuses to loop forever, because a restack that never settles is
// itself the bug.
func (r repo) settle(t *testing.T, stdout string) {
	t.Helper()

	for attempt := 0; strings.Contains(stdout, "Stopped on a conflict"); attempt++ {
		if attempt >= 10 {
			t.Fatalf("restack never settled:\n%s", stdout)
		}
		for _, path := range strings.Fields(r.git("diff", "--name-only", "--diff-filter=U")) {
			// Take the incoming side wholesale: which content wins is not what
			// these scenarios are about, only that the sequence completes.
			r.git("checkout", "--theirs", "--", path)
			r.git("add", path)
		}
		stdout, _, _ = run(t, "restack", "--continue")
	}
}

// leaves names the recorded branches nothing else sits on, deepest last, which
// is one line of descent each.
func (r repo) leaves(t *testing.T) []string {
	t.Helper()
	recorded := r.recordedGraph()
	parents := map[string]bool{}
	for _, edge := range recorded.Branches {
		parents[edge["parent"]] = true
	}
	leaves := make([]string, 0)
	for branch := range recorded.Branches {
		if !parents[branch] {
			leaves = append(leaves, branch)
		}
	}
	sort.Strings(leaves)
	return leaves
}

func (r repo) branchTips() map[string]string {
	r.t.Helper()
	tips := map[string]string{}
	for _, branch := range strings.Fields(r.git("branch", "--format=%(refname:short)")) {
		tips[branch] = r.git("rev-parse", branch)
	}
	return tips
}

func (r repo) subjects(branch string) []string {
	r.t.Helper()
	out := r.git("log", "--format=%s", branch)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// assertInvariants is what every scenario has to satisfy once a restack has
// finished, whatever shape it started as.
func (r repo) assertInvariants(t *testing.T, s scenario, before map[string]string) {
	t.Helper()
	recorded := r.recordedGraph()

	// 1. Every recorded branch sits on the branch recorded as its parent. This
	//    is what a restack is for, and the thing a resumed one used to skip.
	for branch, edge := range recorded.Branches {
		parent := edge["parent"]
		if err := r.ancestor(parent, branch); err != nil {
			t.Errorf("%s does not sit on its recorded parent %s: %v\n%s",
				branch, parent, err, strings.Join(r.subjects(branch), " | "))
		}
	}

	// 2. A trunk is a branch nothing sits under. Anything else means the trunk
	//    set has drifted from the edges, which is what made a straight chain
	//    render as three trunks.
	for _, trunk := range recorded.Trunks {
		if _, tracked := recorded.Branches[trunk]; tracked {
			t.Errorf("%s is recorded as a trunk and also has a parent", trunk)
		}
	}

	// 3. No branch carries the same commit twice. A replay that reapplies work
	//    already upstream looks like success until someone reads the log.
	for branch := range recorded.Branches {
		seen := map[string]int{}
		for _, subject := range r.subjects(branch) {
			seen[subject]++
		}
		for subject, count := range seen {
			if count > 1 && !strings.HasPrefix(subject, "base") {
				t.Errorf("%s carries %q %d times", branch, subject, count)
			}
		}
	}

	// 4. Nothing the user wrote vanished. A branch's own commits survive unless
	//    the trunk absorbed them, which is what landing the base does, or the
	//    user removed them, which a restack must not undo.
	removed := s.droppedSubject()
	for _, c := range s.commits {
		if c.branch == s.merged || c.branch+"/"+c.file == removed {
			continue
		}
		subject := c.branch + "/" + c.file
		if !contains(r.subjects(c.branch), subject) {
			t.Errorf("%s lost %q\n%s", c.branch, subject, strings.Join(r.subjects(c.branch), " | "))
		}
	}

	// 4b. A commit the user removed stays removed. Replaying it back in is the
	//     failure mode a restack is most able to hide, because the branch still
	//     looks healthy and the work is simply back.
	if removed != "" && contains(r.subjects(s.dropped), removed) {
		t.Errorf("%s brought back %q, which was removed by hand\n%s",
			s.dropped, removed, strings.Join(r.subjects(s.dropped), " | "))
	}

	// 5. The operation finished: nothing is left half-done for the next command
	//    to trip over.
	if _, err := os.Stat(filepath.Join(r.dir, ".git", "g2g", "restack.json")); err == nil {
		t.Error("the restack journal survived a completed restack")
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".git", "rebase-merge")); err == nil {
		t.Error("a rebase was left in progress")
	}
}

// ancestor reports whether parent is reachable from branch, which is what "sits
// on" means once the rewrite has happened.
func (r repo) ancestor(parent, branch string) error {
	r.t.Helper()
	return r.repo.Try("merge-base", "--is-ancestor", parent, branch)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
