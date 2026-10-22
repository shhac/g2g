package githubstack

import (
	"context"
	"strings"
	"testing"
)

func TestQueueRequestPinsHeadWithoutChoosingMethod(t *testing.T) {
	runner := &scriptedRunner{responses: []string{`{"data":{"w0":{"clientMutationId":null}}}`}}
	if err := (Client{Runner: runner}).RequestQueue(context.Background(), 41, "PR_synthetic_41", "synthetic-pinned-head"); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(runner.argv[0], " ")
	for _, want := range []string{"enablePullRequestAutoMerge", "expectedHeadOid: $head0", "-f pullRequest0=PR_synthetic_41", "-f head0=synthetic-pinned-head"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q: %s", want, args)
		}
	}
	for _, bad := range []string{"mergeMethod", "--admin", "--squash", "delete"} {
		if strings.Contains(args, bad) {
			t.Fatalf("unexpected %q: %s", bad, args)
		}
	}
}
func TestTargetQueueReadUsesVariableAndParsesPending(t *testing.T) {
	answer := strings.Replace(mergeabilityAnswer, `"squashMergeAllowed":true`, `"targetQueue":{"id":"MQ_synthetic","configuration":{"mergeMethod":"MERGE"}},"squashMergeAllowed":true`, 1)
	answer = strings.Replace(answer, `"mergeCommit":null`, `"mergeQueueEntry":{"id":"MQE_synthetic","state":"AWAITING_CHECKS","headCommit":{"oid":"synthetic-queued-head"}},"autoMergeRequest":{"enabledAt":"2026-01-01T00:00:00Z"},"mergeCommit":null`, 1)
	runner := &scriptedRunner{responses: []string{answer}}
	got, err := (Client{Runner: runner}).Mergeability(context.Background(), []int{41, 42}, "synthetic-future-trunk")
	if err != nil {
		t.Fatal(err)
	}
	if got.Queue == nil || got.Queue.Method() != MethodMerge || !got.States[41].Pending() || got.States[41].QueueEntry.HeadCommit.OID != "synthetic-queued-head" {
		t.Fatalf("readiness=%+v state=%+v", got, got.States[41])
	}
	args := strings.Join(runner.argv[0], " ")
	if !strings.Contains(args, "mergeQueue(branch: $trunk)") || !strings.Contains(args, "-f trunk=synthetic-future-trunk") || strings.Contains(args, "mergeQueue(branch: synthetic") {
		t.Fatal(args)
	}
}
func TestRequiredChecksIgnoreOptionalFailuresAndRetainCompleteness(t *testing.T) {
	for _, more := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "truncated"}[more], func(t *testing.T) {
			page := `false`
			if more {
				page = `true`
			}
			answer := `{"data":{"repository":{"c0":{"statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":` + page + `},"nodes":[{"name":"optional","status":"COMPLETED","conclusion":"FAILURE","isRequired":false},{"name":"build","status":"COMPLETED","conclusion":"FAILURE","isRequired":true},{"name":"lint","status":"COMPLETED","conclusion":"CANCELLED","isRequired":true},{"context":"legacy","state":"PENDING","isRequired":true}]}}}}}}`
			runner := &scriptedRunner{responses: []string{answer}}
			got, err := (Client{Runner: runner}).requiredChecks(context.Background(), []int{41})
			if err != nil {
				t.Fatal(err)
			}
			detail := got[41]
			if detail.Complete == more || strings.Join(detail.Failed, ",") != "build" || strings.Join(detail.Cancelled, ",") != "lint" || strings.Join(detail.Pending, ",") != "legacy" {
				t.Fatalf("checks=%+v", detail)
			}
		})
	}
}

func TestQueueWithoutAutoMergeUsesPinnedEnqueue(t *testing.T) {
	runner := &scriptedRunner{responses: []string{`{"data":{"w0":{"clientMutationId":null}}}`}}
	if err := (Client{Runner: runner}).RequestQueue(context.Background(), 41, "PR_synthetic_41", "synthetic-head", false); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(runner.argv[0], " ")
	if !strings.Contains(args, "enqueuePullRequest") || !strings.Contains(args, "expectedHeadOid: $head0") || strings.Contains(args, "enablePullRequestAutoMerge") || strings.Contains(args, "mergeMethod") {
		t.Fatal(args)
	}
}
