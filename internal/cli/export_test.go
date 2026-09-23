package cli

import (
	"io"
	"time"
)

// The completion ceiling is a keystroke's patience, and the suite spawns fake
// executables that a loaded machine can make slower than that. The ceiling's
// value is asserted on its own; everything else runs without it.
func init() { completionTimeout = time.Minute }

// WriteErrorForTest exposes the top-level error printer to the external
// integration test, which drives the root command rather than Execute and so
// never reaches the process-level printer.
func WriteErrorForTest(writer io.Writer, err error) { writeError(writer, err) }

// SchemaVersion exposes the machine-format version to the external integration
// test, so a bump is asserted against the constant rather than against a
// literal that has to be found and changed alongside it.
const SchemaVersion = schemaVersion

// StoppedPartWayForTest reports an error Execute would exit 3 on, so the
// external tests can assert the status a script reads rather than the prose.
func StoppedPartWayForTest(err error) bool { return wasStopped(err) }

// EveryCommandForTest and CommandPathForTest let the external tests walk the
// command tree and name what they find the way the commands name themselves,
// rather than keeping a second copy of each.
var (
	EveryCommandForTest = everyCommand
	CommandPathForTest  = commandPath
)
