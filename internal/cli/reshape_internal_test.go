package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/reshape"
)

// A removal that finished and could not tidy up, and a rollback that could not
// finish, both leave something done that is not coming back: the part-way
// status, not success and not a failure to retry. A rollback that finished is
// an ordinary failure, because nothing it did is left.
//
// With real Git the tidying step does not fail — releasing a pin that is
// already gone is not an error — so this is asked of the hook directly.
func TestReshapeClaimsOnlyWhatIsLeftPartWay(t *testing.T) {
	cause := errors.New("synthetic failure")
	for _, test := range []struct {
		name    string
		err     error
		stopped bool
	}{
		{name: "a pin left behind", err: &reshape.Partial{Done: "synthetic-a is gone", Left: "its pin stays", Err: cause}, stopped: true},
		{name: "a rollback that could not finish", err: &reshape.RolledBack{Operation: "fold", Branch: "synthetic-a", Cause: cause, Left: cause}, stopped: true},
		{name: "a rollback that finished", err: &reshape.RolledBack{Operation: "fold", Branch: "synthetic-a", Cause: cause}},
		{name: "a plain refusal", err: cause},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			handled, err := reshapeInterrupted(&out, Presentation{})(context.Background(), test.err)

			if handled != test.stopped || wasStopped(err) != test.stopped {
				t.Fatalf("handled %v stopped %v, want %v", handled, wasStopped(err), test.stopped)
			}
			if test.stopped && !strings.Contains(out.String(), "Stopped part-way") {
				t.Errorf("the report does not say it stopped part-way:\n%s", out.String())
			}
		})
	}
}
