package cli

import (
	"context"
	"strings"

	"github.com/shhac/g2g/internal/githubstack"
)

// methodCompletions offers exactly the methods the flag accepts, so completion
// can never propose a value the command would refuse.
func methodCompletions() func(context.Context, string) ([]string, error) {
	return func(context.Context, string) ([]string, error) {
		return githubstack.MethodNames(), nil
	}
}

func methodNames() string {
	return strings.Join(githubstack.MethodNames(), ", ")
}
