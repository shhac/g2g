package stack

// Trunk is the root of the target's full ancestry. Base is only where the
// selection hangs: for branch and subtree scopes it may be a feature parent.
// An older or hand-built snapshot without ancestry uses its base.
func (s Snapshot) Trunk() string {
	if len(s.Ancestry) != 0 {
		return s.Ancestry[0]
	}
	return s.Base
}
