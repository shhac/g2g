package git

import (
	"reflect"
	"testing"
)

func TestOutputLinesTrimsAndDropsEmptyRecords(t *testing.T) {
	output := []byte("\n synthetic-one \n\nsynthetic-two\t\n  \n")
	if got, want := outputLines(output), []string{"synthetic-one", "synthetic-two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outputLines() = %#v, want %#v", got, want)
	}
}
