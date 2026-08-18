package git

import "strings"

// outputLines returns the meaningful lines in command output. Git commonly
// terminates output with a newline and occasionally leaves blank separators;
// callers that parse one record per line should not each make that policy.
func outputLines(output []byte) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
