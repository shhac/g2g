package landed

import "context"

// ParentAbsorption distinguishes work present in the local parent from work
// present only in its known published copy. Only the former is ready to prune.
type ParentAbsorption struct {
	Local, Known Evidence
}

func (a ParentAbsorption) Present() bool { return a.Local == Present || a.Known == Present }

// AssessParent is advisory content evidence, never permission to delete a ref.
// The caller must establish that the merged PR's base is its recorded parent.
func AssessParent(ctx context.Context, probe Probe, parent, branch, knownTip string) ParentAbsorption {
	answer := ParentAbsorption{Local: Assess(ctx, probe, parent, branch, parent)}
	if answer.Local != Present && knownTip != "" && knownTip != parent {
		answer.Known = Assess(ctx, probe, knownTip, branch, parent)
	}
	return answer
}

// ForPublication accepts only evidence about the parent version publication
// leaves behind. Advisory readers may use either copy; submit must pick one.
func (a ParentAbsorption) ForPublication(local bool) bool {
	if local {
		return a.Local == Present
	}
	return a.Known == Present
}

// AssessPublishedParent bounds work at the assessed parent version. The local
// parent may already contain the child's commit ids and would hide work the
// published copy lacks. Missing or unreadable evidence grants no exclusion.
func AssessPublishedParent(ctx context.Context, probe Probe, branch, tip string) Evidence {
	if tip == "" {
		return Unknown
	}
	return Assess(ctx, probe, tip, branch, tip)
}

// KnownParents reads every candidate parent's published copy together, locally.
// An unavailable optional capability gives no evidence and never fetches.
func KnownParents(ctx context.Context, git any, remote string, parents []string) map[string]string {
	reader, ok := git.(interface {
		KnownTips(context.Context, string, []string) (map[string]string, error)
	})
	if !ok || len(parents) == 0 {
		return nil
	}
	tips, err := reader.KnownTips(ctx, remote, parents)
	if err != nil {
		return nil
	}
	return tips
}
