package landed

import "context"

// Evidence distinguishes absent content from an unavailable comparison. The
// zero value provides no evidence; it must never be treated as confirmed absent.
type Evidence uint8

const (
	Unknown Evidence = iota
	Absent
	Present
)

// Assess is Into for advisory reads whose failures mean unknown evidence.
// Destructive decisions continue to use Into and handle its error explicitly.
func Assess(ctx context.Context, probe Probe, base, branch, limit string) Evidence {
	if probe == nil || base == "" {
		return Unknown
	}
	present, err := Into(ctx, probe, base, branch, limit)
	if err != nil {
		return Unknown
	}
	if present {
		return Present
	}
	return Absent
}

// AssessOwn is Assess for a caller that also knows where the branch's own
// work may start, which the limit it bounds the comparison with is not: a
// merged pull request is assessed against the trunk and bounded there, and
// the trunk's tip is no part of the branch. Present is believed only if the
// commits above none of those points would still change base.
func AssessOwn(ctx context.Context, probe Probe, base, branch, limit string, own ...string) Evidence {
	evidence := Assess(ctx, probe, base, branch, limit)
	if evidence == Present && carriesAbove(ctx, probe, base, branch, own) {
		return Absent
	}
	return evidence
}
