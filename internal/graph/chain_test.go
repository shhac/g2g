package graph

import (
	"strings"
	"testing"
)

// linear is what a stack looks like when measured from its tip: each ancestor
// one commit further away than the last.
func linear() []Candidate {
	return []Candidate{
		{Branch: "synthetic-b", Distance: 1, Ancestor: true},
		{Branch: "synthetic-a", Distance: 2, Ancestor: true},
		{Branch: "synthetic-trunk", Distance: 3, Ancestor: true, Trunk: true},
	}
}

// The chain runs trunk first, so a parent is always recorded before the
// branches that name it.
func TestChainOrdersFromTheTrunkDown(t *testing.T) {
	chain, err := Chain(linear(), "synthetic-trunk")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if got, want := strings.Join(chain, ","), "synthetic-trunk,synthetic-a,synthetic-b"; got != want {
		t.Errorf("Chain() = %s, want %s", got, want)
	}
}

// Stopping at a nearer trunk records less, not more: everything below it is
// somebody else's business.
func TestChainStopsAtTheTrunkItWasGiven(t *testing.T) {
	chain, err := Chain(linear(), "synthetic-a")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if got, want := strings.Join(chain, ","), "synthetic-a,synthetic-b"; got != want {
		t.Errorf("Chain() = %s, want %s", got, want)
	}
}

// A branch the target cannot reach is not on the way to anywhere, so it is not
// part of the chain and cannot be the trunk either.
func TestChainIgnoresBranchesTheTargetCannotReach(t *testing.T) {
	candidates := append(linear(), Candidate{Branch: "synthetic-elsewhere", Distance: 1})
	chain, err := Chain(candidates, "synthetic-trunk")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if strings.Contains(strings.Join(chain, ","), "elsewhere") {
		t.Errorf("Chain() = %v, want the unreachable branch left out", chain)
	}
	if _, err := Chain(candidates, "synthetic-elsewhere"); err == nil {
		t.Error("Chain() error = nil for a trunk the target cannot reach")
	}
}

// Equal distance means neither branch is above the other, which is a fork and
// not a stack. Deriving an order would be the guess this refuses to make.
func TestChainRefusesAnAmbiguousOrder(t *testing.T) {
	tiedCandidates := []Candidate{
		{Branch: "synthetic-b", Distance: 1, Ancestor: true},
		{Branch: "synthetic-c", Distance: 1, Ancestor: true},
		{Branch: "synthetic-trunk", Distance: 3, Ancestor: true, Trunk: true},
	}

	_, err := Chain(tiedCandidates, "synthetic-trunk")
	if err == nil {
		t.Fatal("Chain() error = nil for two branches at the same distance")
	}
	for _, want := range []string{"synthetic-b", "synthetic-c", "g2g track --parent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to contain %q", err, want)
		}
	}
}

func TestChainRefusesATrunkThatIsNotAnAncestor(t *testing.T) {
	if _, err := Chain(linear(), "synthetic-absent"); err == nil {
		t.Error("Chain() error = nil for a trunk that is not on the ancestry")
	}
}

// Only a branch the graph already treats as a root may end an adoption. Picking
// any other would be deciding where someone's stack begins.
func TestTrunkForTakesTheOnlyRecordedRootOnTheAncestry(t *testing.T) {
	trunk, err := TrunkFor(linear(), []string{"synthetic-trunk", "synthetic-unrelated"})
	if err != nil {
		t.Fatalf("TrunkFor() error = %v", err)
	}
	if trunk != "synthetic-trunk" {
		t.Errorf("TrunkFor() = %q", trunk)
	}
}

func TestTrunkForRefusesWhenItCannotBeSureAlone(t *testing.T) {
	if _, err := TrunkFor(linear(), nil); err == nil {
		t.Error("TrunkFor() error = nil with no recorded root on the ancestry")
	}
	_, err := TrunkFor(linear(), []string{"synthetic-trunk", "synthetic-a"})
	if err == nil {
		t.Fatal("TrunkFor() error = nil with two recorded roots on the ancestry")
	}
	if !strings.Contains(err.Error(), "--trunk") {
		t.Errorf("error = %v, want it to name the flag that resolves it", err)
	}
}

// compare is the decision matrix a bulk adoption runs on, and it needs no Git.
func TestCompareSplitsTheDerivedStructureIntoRecordAgreeAndConflict(t *testing.T) {
	adopted := Graph{
		Edges: map[string]Edge{
			"synthetic-a": {Parent: "synthetic-trunk"},
			"synthetic-b": {Parent: "synthetic-elsewhere"},
		},
		Trunks: []string{"synthetic-trunk"},
	}

	record, already, conflicts := compare(adopted, []string{"synthetic-trunk", "synthetic-a", "synthetic-b", "synthetic-c"}, nil)

	if got := strings.Join(already, ","); got != "synthetic-a" {
		t.Errorf("already = %s, want the edge both agree on", got)
	}
	if got := strings.Join(conflicts, ","); got != "synthetic-b" {
		t.Errorf("conflicts = %s, want the edge recorded differently", got)
	}
	if len(record) != 1 || record[0].Branch != "synthetic-c" || record[0].Parent != "synthetic-b" {
		t.Errorf("record = %+v, want only the missing edge", record)
	}
}

// A stack is a forest, not a list. Branches hanging off the spine attach to it,
// and branches hanging off those attach in turn.
func TestAttachJoinsABranchToTheSelectedSpine(t *testing.T) {
	candidates := []Candidate{
		{Branch: "synthetic-a", Distance: 1, Ancestor: true},
		{Branch: "synthetic-trunk", Distance: 4, Ancestor: true, Trunk: true},
	}

	parent, attached, err := Attach(candidates, []string{"synthetic-a", "synthetic-b"})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if !attached || parent != "synthetic-a" {
		t.Errorf("Attach() = %q, %t; want synthetic-a", parent, attached)
	}
}

// A branch sitting directly on the trunk is a separate stack that happens to
// share a base. Sweeping it in because it is technically a descendant would
// adopt half the repository.
func TestAttachLeavesBranchesThatOnlyShareTheTrunk(t *testing.T) {
	candidates := []Candidate{{Branch: "synthetic-trunk", Distance: 1, Ancestor: true, Trunk: true}}

	_, attached, err := Attach(candidates, []string{"synthetic-a", "synthetic-b"})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if attached {
		t.Error("Attach() joined a branch whose only selected ancestor is the trunk")
	}
}

// Two possible parents at the same distance is a guess, and this refuses to
// make it for the same reason track does.
func TestAttachRefusesTwoEquallyNearParents(t *testing.T) {
	candidates := []Candidate{
		{Branch: "synthetic-a", Distance: 1, Ancestor: true},
		{Branch: "synthetic-b", Distance: 1, Ancestor: true},
	}

	_, _, err := Attach(candidates, []string{"synthetic-a", "synthetic-b"})
	if err == nil {
		t.Fatal("Attach() error = nil for two equally near parents")
	}
	if !strings.Contains(err.Error(), "g2g track --parent") {
		t.Errorf("error = %v, want it to name the way out", err)
	}
}
