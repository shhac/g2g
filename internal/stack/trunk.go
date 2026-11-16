package stack

// Trunk is the source-resolved landing boundary. An unresolved boundary stays
// unknown; ancestry is revalidation data, not a landing decision. Older
// hand-built snapshots without ancestry use Base.
func (s Snapshot) Trunk() string {
	if s.LandingTrunk != "" || len(s.Ancestry) != 0 {
		return s.LandingTrunk
	}
	return s.Base
}
