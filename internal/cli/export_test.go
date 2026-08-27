package cli

import "io"

// WriteErrorForTest exposes the top-level error printer to the external
// integration test, which drives the root command rather than Execute and so
// never reaches the process-level printer.
func WriteErrorForTest(writer io.Writer, err error) { writeError(writer, err) }

// SchemaVersion exposes the machine-format version to the external integration
// test, so a bump is asserted against the constant rather than against a
// literal that has to be found and changed alongside it.
const SchemaVersion = schemaVersion
