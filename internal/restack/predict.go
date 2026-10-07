package restack

import (
	"context"

	localgit "github.com/shhac/g2g/internal/git"
)

// preview asks the replay engine what the rewrite would produce, without
// producing it. A repository whose Git cannot replay gets no prediction, which
// costs the conflict warning but nothing else.
func (s Service) preview(ctx context.Context, plan Plan) (updates []localgit.RefUpdate, clean bool, unpredicted string, err error) {
	supported, err := s.Git.SupportsReplay(ctx)
	if err != nil {
		return nil, false, "", err
	}
	if !supported {
		return nil, false, "this Git cannot preview the result", nil
	}
	// Every step collapsing leaves no group at all: each branch's work is
	// already in its new base by content, so their refs move and nothing is
	// replayed. There is nothing to predict and nothing that could conflict,
	// and asking the engine anyway failed the whole command with "no commit
	// ranges selected for replay" — on precisely the case where a stack has
	// finished landing.
	//
	// A root behind its parent lands on the parent's result, which only the
	// parent's own preview knows. It prints the object for a branch it names;
	// a parent the caller is replacing is named by object instead and prints
	// nothing, and then there is no honest prediction to make.
	clean = true
	replayedTo := map[string]string{}
	for _, group := range plan.groups() {
		onto := group.onto()
		if group[0].Behind {
			tip, known := replayedTo[group[0].Parent]
			if !known {
				return nil, false, group[0].Branch + " lands on " + group[0].Parent + " as it will be once brought down, which cannot be previewed", nil
			}
			onto = tip
		}
		grouped, groupClean, err := s.Git.PreviewReplay(ctx, onto, group.previewed())
		if err != nil {
			return nil, false, "", err
		}
		if !groupClean {
			// A conflict anywhere sends the whole rewrite to the resumable
			// engine, so what the groups above it would do is moot.
			return nil, false, "", nil
		}
		updates = append(updates, grouped...)
		for _, update := range grouped {
			replayedTo[update.Branch()] = update.New
		}
	}
	// Predicted means the preview actually ran, which the two returns above
	// already answer for the cases where it did not. Deriving it from the error
	// being returned alongside read as though a caller might see both, when the
	// only caller bails on the error first.
	return updates, clean, "", nil
}
