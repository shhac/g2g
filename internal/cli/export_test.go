package cli

import "io"

// WriteErrorForTest exposes the top-level error printer to the external
// integration test, which drives the root command rather than Execute and so
// never reaches the process-level printer.
func WriteErrorForTest(writer io.Writer, err error) { writeError(writer, err) }
