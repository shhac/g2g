package testutil

import "errors"

// ErrReplanned is what Replan answers when the second plan differs.
var ErrReplanned = errors.New("plan changed during revalidation")

// Replan is a package's half of what an apply does before mutating: plan
// again and compare with the preview. The CLI's flow does the asking for
// every command; a package test asks the same question of its own plan.
//
//	current, err := testutil.Replan(preview)(service.Plan(ctx, selection))
func Replan[P interface{ Equal(P) bool }](preview P) func(P, error) (P, error) {
	return func(current P, err error) (P, error) {
		if err != nil {
			return current, err
		}
		if !current.Equal(preview) {
			return current, ErrReplanned
		}
		return current, nil
	}
}
