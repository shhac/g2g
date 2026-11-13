package cli

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shhac/g2g/internal/githubstack"
	"github.com/shhac/g2g/internal/push"
	"github.com/shhac/g2g/internal/stack"
	"github.com/shhac/g2g/internal/submit"
	"github.com/spf13/cobra"
)

type submitGuardGit struct{ submit.Git }

func (submitGuardGit) Remote(context.Context, string) error { return nil }

type submitGuardSelector struct{ reads *int }

func (s submitGuardSelector) Select(context.Context, stack.Selection, string) (stack.Snapshot, error) {
	*s.reads++
	return stack.Snapshot{}, errors.New("HEAD is detached and no branch was selected")
}

func TestSubmitApplyGuardsBeforeInitialDiscoveryButPreviewCanRead(t *testing.T) {
	for _, apply := range []bool{true, false} {
		reads, guards := 0, 0
		service := submit.Service{Git: submitGuardGit{}, Selector: submitGuardSelector{&reads}, GitHub: githubstack.Client{}, Pusher: &push.Service{}}
		options := submitOptions{apply: apply, guard: guardFunc(func() error {
			guards++
			return errors.New("restack belongs to checkout synthetic-owner")
		})}
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		cmd.SetOut(io.Discard)
		err := options.run(cmd, service, Presentation{})
		if apply {
			if err == nil || !strings.Contains(err.Error(), "synthetic-owner") || guards != 1 || reads != 0 {
				t.Fatalf("apply error=%v guards=%d reads=%d", err, guards, reads)
			}
		} else if err == nil || !strings.Contains(err.Error(), "HEAD is detached") || guards != 0 || reads != 1 {
			t.Fatalf("preview error=%v guards=%d reads=%d", err, guards, reads)
		}
	}
}
