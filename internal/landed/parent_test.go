package landed

import "testing"

func TestParentAbsorptionUsesTheVersionPublicationLeaves(t *testing.T) {
	for _, local := range []Evidence{Unknown, Absent, Present} {
		for _, known := range []Evidence{Unknown, Absent, Present} {
			answer := ParentAbsorption{Local: local, Known: known}
			if answer.ForPublication(true) != (local == Present) || answer.ForPublication(false) != (known == Present) {
				t.Fatalf("wrong projected eligibility: %+v", answer)
			}
		}
	}
}
