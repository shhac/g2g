package landed

import (
	"context"
	"errors"
	"testing"
)

type evidenceProbe struct {
	absent     []string
	absorbed   bool
	err        error
	byBase     map[string]Evidence
	asked      *[][3]string
	known      map[string]string
	knownReads *int
}

func (p evidenceProbe) Cherry(_ context.Context, base, head, limit string) ([]string, []string, error) {
	if p.asked != nil {
		*p.asked = append(*p.asked, [3]string{base, head, limit})
	}
	if p.byBase != nil {
		switch p.byBase[base] {
		case Present:
			return nil, nil, nil
		case Absent:
			return []string{"synthetic-commit"}, nil, nil
		default:
			return nil, nil, errors.New("synthetic unavailable base")
		}
	}
	return p.absent, nil, p.err
}
func (p evidenceProbe) Absorbed(context.Context, string, string) (bool, error) {
	return p.absorbed, p.err
}

func (p evidenceProbe) KnownTips(context.Context, string, []string) (map[string]string, error) {
	if p.knownReads != nil {
		*p.knownReads++
	}
	return p.known, p.err
}

func TestAssessParentUsesLocalThenKnownContent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		local, known Evidence
		want         ParentAbsorption
		reads        int
	}{
		{"local", Present, Absent, ParentAbsorption{Local: Present}, 1},
		{"known published", Absent, Present, ParentAbsorption{Local: Absent, Known: Present}, 2},
		{"neither", Absent, Absent, ParentAbsorption{Local: Absent, Known: Absent}, 2},
		{"unknown", Unknown, Unknown, ParentAbsorption{}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := [][3]string{}
			probe := evidenceProbe{byBase: map[string]Evidence{"synthetic-parent": tc.local, "synthetic-known": tc.known}, asked: &asked}
			got := AssessParent(context.Background(), probe, "synthetic-parent", "synthetic-child", "synthetic-known")
			if got != tc.want || len(asked) != tc.reads {
				t.Fatalf("evidence = %+v, reads = %v, want %+v (%d)", got, asked, tc.want, tc.reads)
			}
			for _, query := range asked {
				if query[1] != "synthetic-child" || query[2] != "synthetic-parent" {
					t.Errorf("unbounded parent query: %v", query)
				}
			}
		})
	}
}

func TestKnownParentsIsOneOptionalLocalRead(t *testing.T) {
	reads := 0
	probe := evidenceProbe{known: map[string]string{"synthetic-parent": "synthetic-tip"}, knownReads: &reads}
	if got := KnownParents(context.Background(), probe, "synthetic-remote", []string{"synthetic-parent", "synthetic-other"}); got["synthetic-parent"] != "synthetic-tip" || reads != 1 {
		t.Fatalf("tips = %v, reads = %d", got, reads)
	}
	if got := KnownParents(context.Background(), probe, "origin", nil); got != nil || reads != 1 {
		t.Fatal("empty candidates read Git")
	}
	if got := KnownParents(context.Background(), struct{}{}, "origin", []string{"synthetic-parent"}); got != nil {
		t.Fatal("missing capability supplied evidence")
	}
	probe.err = errors.New("synthetic read failure")
	if got := KnownParents(context.Background(), probe, "origin", []string{"synthetic-parent"}); got != nil {
		t.Fatal("failed read supplied evidence")
	}
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
