package githubstack

import (
	"slices"
	"testing"
)

func TestRequiredCheckResultPrecedence(t *testing.T) {
	for _, test := range []struct {
		name                       string
		check                      checkNode
		failed, cancelled, pending []string
	}{
		{name: "cancellation beats failure and pending", check: checkNode{Name: "synthetic-check", Required: true, Conclusion: "CANCELLED", State: "ERROR", Status: "IN_PROGRESS"}, cancelled: []string{"synthetic-check"}},
		{name: "legacy failure beats pending", check: checkNode{Name: "synthetic-check", Required: true, State: "FAILURE", Status: "QUEUED"}, failed: []string{"synthetic-check"}},
		{name: "legacy error beats pending", check: checkNode{Name: "synthetic-check", Required: true, State: "ERROR", Status: "IN_PROGRESS"}, failed: []string{"synthetic-check"}},
		{name: "optional failure is ignored", check: checkNode{Name: "synthetic-check", Conclusion: "FAILURE", Status: "QUEUED"}},
		{name: "legacy context supplies its name", check: checkNode{Context: "synthetic-legacy", Required: true, State: "PENDING"}, pending: []string{"synthetic-legacy"}},
		{name: "running required check is pending", check: checkNode{Name: "synthetic-check", Required: true, Status: "IN_PROGRESS"}, pending: []string{"synthetic-check"}},
		{name: "unknown completed conclusion stays unknown", check: checkNode{Name: "synthetic-check", Required: true, Conclusion: "SYNTHETIC_FUTURE_RESULT", Status: "COMPLETED"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, more := range []bool{false, true} {
				got := classifyChecks([]checkNode{test.check}, more)
				if got.Complete == more || !slices.Equal(got.Failed, test.failed) || !slices.Equal(got.Cancelled, test.cancelled) || !slices.Equal(got.Pending, test.pending) {
					t.Fatalf("more=%t: checks=%+v", more, got)
				}
			}
		})
	}
	for _, conclusion := range []string{"FAILURE", "TIMED_OUT", "ACTION_REQUIRED", "STARTUP_FAILURE"} {
		t.Run(conclusion, func(t *testing.T) {
			got := classifyChecks([]checkNode{{Name: "synthetic-check", Required: true, Conclusion: conclusion, Status: "QUEUED"}}, false)
			if !slices.Equal(got.Failed, []string{"synthetic-check"}) || len(got.Pending) != 0 {
				t.Fatalf("failure read as pending: %+v", got)
			}
		})
	}
}
