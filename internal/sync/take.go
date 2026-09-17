package sync

import (
	"fmt"
	"strings"
)

// Which side wins where sync would otherwise refuse.
//
// Parsing and error prose, no Git — the one part of this package that answers
// a question about a flag rather than about a repository.

// Take is which side wins where sync would otherwise refuse.
//
// It is an enum rather than a boolean flag because the question has more
// answers than the one implemented, and naming the value leaves room for them.
// There is deliberately no "mine": sync only ever moves toward this checkout
// and push only ever moves toward the remote, so which side wins is normally
// answered by which command you run. This exists for the case neither command
// can otherwise reach.
type Take string

const (
	// TakeNothing is the default: a divergence that would cost commits is
	// reported rather than resolved.
	TakeNothing Take = ""
	// TakePublished discards local commits the published version does not have,
	// on the branches where the two have genuinely diverged.
	TakePublished Take = "published"
)

// Takes are the values a caller may name.
var Takes = []Take{TakePublished}

// ParseTake validates a flag value.
func ParseTake(value string) (Take, error) {
	if value == "" {
		return TakeNothing, nil
	}
	for _, take := range Takes {
		if Take(value) == take {
			return take, nil
		}
	}
	names := make([]string, 0, len(Takes))
	for _, take := range Takes {
		names = append(names, string(take))
	}
	return TakeNothing, fmt.Errorf("unsupported --take %q (want %s)", value, strings.Join(names, ", "))
}
