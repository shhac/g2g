package cli

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/shhac/gt2gh/internal/link"
	"github.com/shhac/gt2gh/internal/push"
)

// These constructors exist only for tests, which is why they live here. They
// used to sit in cli.go alongside the production entry points, where three
// exported-but-unused-in-production constructors made the real wiring hard to
// pick out.

func NewWithService(version string, stdout, stderr io.Writer, service link.Service) *cobra.Command {
	return NewWithOptions(Options{Version: version, Stdout: stdout, Stderr: stderr, Link: service})
}

func newWithPresentation(version, commandName string, stdout, stderr io.Writer, service link.Service, pushService push.Service, presentation Presentation) *cobra.Command {
	return NewWithOptions(Options{
		Version: version, CommandName: commandName, Stdout: stdout, Stderr: stderr,
		Link: service, Push: pushService, Presentation: &presentation,
	})
}
