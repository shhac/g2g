package cli

import (
	"github.com/spf13/cobra"

	localgit "github.com/shhac/g2g/internal/git"
)

// registerNoSetUpstream declares the opt-out on every command that publishes,
// so push, submit and land spell and explain it the same way.
func registerNoSetUpstream(cmd *cobra.Command, into *bool) {
	cmd.Flags().BoolVar(into, "no-set-upstream", false, "leave each pushed branch's upstream as it is instead of tracking the remote's copy")
}

func upstreamFor(noSetUpstream bool) localgit.Upstream {
	if noSetUpstream {
		return localgit.LeaveUpstream
	}
	return localgit.SetUpstream
}
