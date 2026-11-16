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
