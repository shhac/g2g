package cli

import (
	"testing"

	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/submit"
)

// submit owns no rule about what may be published, so --strict is push's:
// the run gets a strict copy of the push it publishes through, and the push
// every other command shares is left as it was.
func TestStrictSubmitPublishesThroughAStrictCopyOfPush(t *testing.T) {
	shared := &push.Service{}
	service := submit.Service{Pusher: shared}

	if got := strictSubmit(service, false); got.Pusher != shared {
		t.Error("a submit without --strict did not publish through the shared push")
	}
	strict := strictSubmit(service, true)
	pusher, ok := strict.Pusher.(*push.Service)
	if !ok || !pusher.Strict {
		t.Fatalf("Pusher = %#v, want a strict push", strict.Pusher)
	}
	if shared.Strict || service.Pusher != shared {
		t.Error("--strict changed the push every other command shares")
	}
}
