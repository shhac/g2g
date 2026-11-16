package syncpoint

import (
	"reflect"
	"testing"

	localgit "github.com/shhac/g2g/internal/git"
)

func TestLevelRequiresNonemptyExactObservedEquality(t *testing.T) {
	local := map[string]string{"synthetic-level": "synthetic-tip", "synthetic-ahead": "synthetic-new", "synthetic-behind": "synthetic-old", "synthetic-empty": "", "synthetic-absent": "synthetic-tip"}
	published := map[string]string{"synthetic-level": "synthetic-tip", "synthetic-ahead": "synthetic-old", "synthetic-behind": "synthetic-new", "synthetic-empty": "", "synthetic-remote-only": "synthetic-tip"}
	want := map[string]localgit.SyncPoint{"synthetic-level": {Tip: "synthetic-tip", Local: "synthetic-tip", Command: "synthetic-command"}}
	if got := Level("synthetic-command", local, published); !reflect.DeepEqual(got, want) {
		t.Fatalf("Level = %+v, want %+v", got, want)
	}
	if len(Level("synthetic-command", nil, nil)) != 0 || local["synthetic-ahead"] != "synthetic-new" || published["synthetic-ahead"] != "synthetic-old" {
		t.Fatal("inputs changed or missing evidence recorded")
	}
}
