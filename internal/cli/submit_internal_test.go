package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/submit"
)

// submit owns no rule about what may be published, so --strict is push's:
// the push submit plans is made strict, and nothing else about it changes.
func TestStrictSubmitRefusesThroughItsPush(t *testing.T) {
	plan := submit.Plan{Push: push.Plan{Strict: repair.Note{Reason: "--strict: it would drop synthetic-b 0123456789ab"}}}

	if got := (submitOptions{}).planned(plan); got.Blocked() != "" {
		t.Errorf("a submit without --strict refused: %s", got.Blocked())
	}
	if got := (submitOptions{strict: true}).planned(plan); got.Blocked() != "--strict: it would drop synthetic-b 0123456789ab" {
		t.Errorf("Blocked() = %q, want the strict refusal", got.Blocked())
	}
}
