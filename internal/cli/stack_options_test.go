package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/cli"
)

// commandsOffering names every registered command carrying a given flag.
//
// It walks the real command tree rather than a list, so a command added later
// is covered without anybody remembering to add it here — which is the failure
// both guards below exist to catch.
func commandsOffering(t *testing.T, flag string) []string {
	t.Helper()

	var offering []string
	var discard bytes.Buffer
	for _, cmd := range cli.New("v0.0.0-test", &discard, &discard).Commands() {
		if cmd.Flags().Lookup(flag) != nil {
			offering = append(offering, cmd.Name())
		}
	}
	if len(offering) == 0 {
		t.Fatalf("no command offers --%s; this guard would pass vacuously", flag)
	}
	return offering
}

// A command that offers --from must validate it, and validating the scope is
// not the same thing. The two were separate calls picked by hand, which is how
// push came to honour a source it must never reach; sharing one validate()
// only helps if every such command actually calls it.
func TestEveryCommandOfferingFromValidatesIt(t *testing.T) {
	for _, name := range commandsOffering(t, "from") {
		t.Run(name, func(t *testing.T) {
			recorder, _ := g2gOwnedRepository(t, ownedGraph)

			_, _, err := run(t, name, "--from", "synthetic-nonsense")
			if err == nil {
				t.Fatalf("%s accepted --from synthetic-nonsense", name)
			}
			if !strings.Contains(err.Error(), "unknown source") {
				t.Errorf("%s did not refuse the source by name: %v", name, err)
			}
			// The resolver refuses an unknown source too, so the error alone
			// cannot tell the flag's check from the resolver's. What
			// distinguishes them is when: a command that validates its own
			// flags has not resolved a branch yet, so nothing has run.
			recorder.AssertNone("git ", "gh ", "gt ")
		})
	}
}

// A command that offers --scope must validate that too, and for the same
// reason: Cobra checks a flag's syntax and never its vocabulary.
func TestEveryCommandOfferingScopeValidatesIt(t *testing.T) {
	for _, name := range commandsOffering(t, "scope") {
		t.Run(name, func(t *testing.T) {
			recorder, _ := g2gOwnedRepository(t, ownedGraph)

			_, _, err := run(t, name, "--scope", "synthetic-nonsense")
			if err == nil {
				t.Fatalf("%s accepted --scope synthetic-nonsense", name)
			}
			if !strings.Contains(err.Error(), "scope") {
				t.Errorf("%s did not refuse the scope by name: %v", name, err)
			}
			recorder.AssertNone("git ", "gh ", "gt ")
		})
	}
}
