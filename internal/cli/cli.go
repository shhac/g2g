// Package cli parses and dispatches gt2gh commands.
package cli

import (
	"errors"
	"fmt"
	"io"
)

var errUsage = errors.New("invalid command; run 'gt2gh --help' for usage")

const usage = `Usage:
  gt2gh link

Commands:
  link      Link a linear Graphite stack to GitHub (planned; no changes yet)

Options:
  -h, --help       Show this help message
      --version    Show the version
`

// Run parses args and writes command output to stdout. It deliberately does
// not invoke gt or gh yet: the link workflow is only a safe placeholder.
func Run(args []string, stdout io.Writer, version string) error {
	switch len(args) {
	case 0:
		_, _ = io.WriteString(stdout, usage)
		return nil
	case 1:
		switch args[0] {
		case "-h", "--help", "help":
			_, _ = io.WriteString(stdout, usage)
			return nil
		case "--version":
			_, _ = fmt.Fprintln(stdout, version)
			return nil
		case "link":
			_, _ = fmt.Fprintln(stdout, "gt2gh link is not implemented yet; no commands were run.")
			return nil
		default:
			return errUsage
		}
	case 2:
		if args[0] == "link" && (args[1] == "-h" || args[1] == "--help") {
			_, _ = io.WriteString(stdout, "Usage: gt2gh link\n\nThis command is planned but not implemented yet.\n")
			return nil
		}
		fallthrough
	default:
		return errUsage
	}
}
