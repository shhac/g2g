package landed

import (
	"context"
	"errors"
	"testing"
)

type evidenceProbe struct {
	absent   []string
	absorbed bool
	err      error
}

func (p evidenceProbe) Cherry(context.Context, string, string, string) ([]string, []string, error) {
	return p.absent, nil, p.err
}
func (p evidenceProbe) Absorbed(context.Context, string, string) (bool, error) {
	return p.absorbed, p.err
}

func TestAssessEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		probe Probe
		want  Evidence
	}{
		{"no probe", nil, Unknown},
		{"unreadable", evidenceProbe{err: errors.New("synthetic read failure")}, Unknown},
		{"commit present", evidenceProbe{}, Present},
		{"squash present", evidenceProbe{absent: []string{"synthetic-commit"}, absorbed: true}, Present},
		{"absent", evidenceProbe{absent: []string{"synthetic-commit"}}, Absent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Assess(context.Background(), tc.probe, "synthetic-trunk", "synthetic-head", "synthetic-trunk"); got != tc.want {
				t.Fatalf("evidence=%v; want %v", got, tc.want)
			}
		})
	}
}
