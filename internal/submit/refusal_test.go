package submit

import (
	"context"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/stack"
)

func TestRefusalPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		link, preserved, issues, push bool
		want                          string
	}{
		{name: "link first", link: true, preserved: true, issues: true, push: true, want: "linking"},
		{name: "preserved before issues", preserved: true, issues: true, push: true, want: "synthetic preserved"},
		{name: "issues before push", issues: true, push: true, want: "existing pull request state: synthetic-head: synthetic ambiguous"},
		{name: "push", push: true, want: "synthetic push"},
		{name: "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := Plan{Snapshot: stack.Snapshot{Target: "synthetic-root", Base: "synthetic-trunk", Branches: []string{"synthetic-root", "synthetic-a", "synthetic-b"}, Parents: map[string]string{"synthetic-root": "synthetic-trunk", "synthetic-a": "synthetic-root", "synthetic-b": "synthetic-root"}}}
			if tc.preserved {
				plan.Repair = repair.Note{Reason: "synthetic preserved"}
			}
			if tc.issues {
				plan.Issues = map[string]string{"synthetic-head": "synthetic ambiguous"}
			}
			if tc.push {
				plan.Push.Repair = repair.Note{Reason: "synthetic push"}
			}
			note := plan.Refusal(tc.link)
			if tc.want == "" {
				if note.Sentence() != "" {
					t.Fatal(note)
				}
				return
			}
			if !strings.Contains(note.Sentence(), tc.want) {
				t.Fatalf("refusal=%s; want %s", note.Sentence(), tc.want)
			}
			git := &fakeGit{}
			err := (Service{Pusher: git}).Apply(context.Background(), plan, NewSpec(nil, ""), tc.link)
			if err == nil || !strings.Contains(err.Error(), note.Sentence()) || git.pushes != 0 {
				t.Fatalf("Apply disagrees: %v, pushes=%d", err, git.pushes)
			}
			if !tc.link && plan.Blocked() != note.Sentence() {
				t.Fatal("Blocked disagrees with Refusal")
			}
		})
	}
}
