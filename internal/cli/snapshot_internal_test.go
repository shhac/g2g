package cli

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/shhac/g2g/internal/graph"
	"github.com/shhac/g2g/internal/prune"
	"github.com/shhac/g2g/internal/repair"
	"github.com/shhac/g2g/internal/restack"
)

// snapshotted are the plan types written to a snapshot file as they are.
var snapshotted = map[string]reflect.Type{
	"prune":   reflect.TypeFor[prune.Plan](),
	"restack": reflect.TypeFor[restack.Plan](),
}

// lost lists the fields of a type that a JSON file would not hold: unexported
// ones, which are dropped, and ones only known through an interface, a
// function or a channel, which cannot be read back.
func lost(typ reflect.Type, path string, seen map[reflect.Type]bool) []string {
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return lost(typ.Elem(), path+"[]", seen)
	case reflect.Map:
		return append(lost(typ.Key(), path+"{key}", seen), lost(typ.Elem(), path+"{}", seen)...)
	case reflect.Interface, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return []string{fmt.Sprintf("%s is a %s", path, typ.Kind())}
	case reflect.Struct:
		if seen[typ] {
			return nil
		}
		seen[typ] = true
		fields := make([]string, 0)
		for index := range typ.NumField() {
			field := typ.Field(index)
			if !field.IsExported() {
				fields = append(fields, fmt.Sprintf("%s.%s is unexported", path, field.Name))
				continue
			}
			fields = append(fields, lost(field.Type, path+"."+field.Name, seen)...)
		}
		return fields
	}
	return nil
}

// A plan is written to a snapshot as it is and applied from what is read
// back, with no planning to put right what the file did not hold. A field
// that would not survive comes back zero, silently, in the plan that is then
// carried out -- so adding one to a snapshotted plan has to fail here.
func TestEverySnapshottedPlanSurvivesBeingWritten(t *testing.T) {
	for name, typ := range snapshotted {
		for _, field := range lost(typ, name, map[reflect.Type]bool{}) {
			t.Errorf("a snapshot would lose %s", field)
		}
	}
}

func TestAPrunePlanReadBackIsThePlanThatWasWritten(t *testing.T) {
	recorded := graph.Graph{Trunks: []string{"synthetic-main"}, Edges: map[string]graph.Edge{
		"synthetic-a": {Parent: "synthetic-main", ForkPoint: "fork-a", Origin: graph.OriginAncestry},
		"synthetic-b": {Parent: "synthetic-a", ForkPoint: "fork-b"},
	}}
	plan := prune.Plan{
		Discovery: graph.Discovery{
			Target: "synthetic-b", Scope: graph.ScopeStack, Graph: recorded, StorePath: "synthetic/graph.json",
			Branches: []string{"synthetic-main", "synthetic-a", "synthetic-b"},
			States:   map[string]graph.NodeState{"synthetic-a": graph.StateLanded, "synthetic-b": graph.StateAligned},
		},
		Landed:  []string{"synthetic-a"},
		Missing: []string{"synthetic-gone"},
		Options: prune.Options{DeleteBranches: true, ForgetMissing: true},
		Delete:  map[string]string{"synthetic-a": "tip-a"},
		Tips:    map[string]string{"synthetic-a": "tip-a", "synthetic-main": "tip-main"},
		Rehome:  map[string]graph.Edge{"synthetic-b": {Parent: "synthetic-main", ForkPoint: "tip-main", Origin: graph.OriginAncestry}},
		Kept:    repair.Note{Reason: "synthetic reason", Ways: []repair.Step{{Command: "g2g synthetic", Effect: "synthetic effect"}}},
	}
	written, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var read prune.Plan
	if err := json.Unmarshal(written, &read); err != nil {
		t.Fatal(err)
	}
	if !plan.Equal(read) || !reflect.DeepEqual(plan, read) {
		t.Errorf("read back:\n%+v\nwritten:\n%+v", read, plan)
	}
}
