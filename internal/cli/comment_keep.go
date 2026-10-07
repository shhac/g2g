package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/shhac/g2g/internal/comment"
	"github.com/shhac/g2g/internal/stack"
)

// Keeping the stack comments as the tail of another command.
//
// submit and land change which pull requests a stack is made of, so they keep
// the map on each of them in step unless --no-comment says not to. It is the
// comment command's own planning and writing, run once the command's own work
// is done; a build without a comment service simply does not do it.

// keepComments keeps the comments for each named branch's stack, and does
// nothing when the command was told not to or cannot.
func keepComments(ctx context.Context, service comment.Service, keep bool, selections ...stack.Selection) error {
	if !keep || !service.Ready() {
		return nil
	}
	for _, selection := range selections {
		if _, err := service.Keep(ctx, selection); err != nil {
			return err
		}
	}
	return nil
}

// commentsNotKept reports a command whose own work stands and whose comments
// could not be kept, and marks it as stopped part-way: the work is not coming
// back, and the comments still need keeping.
//
// retry is the comment run that finishes the job, aimed at the stack the
// command acted on where it still exists.
func commentsNotKept(cmd *cobra.Command, err error, retry string, p Presentation) (bool, error) {
	return claim(err, func(notKept *comment.NotKept) error {
		if err := prose(cmd.OutOrStdout(), p, "\n"+p.problem("Done, but "+notKept.Error()+".")); err != nil {
			return err
		}
		return writeWhatStands(cmd, p, "Everything else stands. Run "+runnable(retry)+" to keep them.", notKept)
	})
}
