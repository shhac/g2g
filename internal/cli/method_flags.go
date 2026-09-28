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
		return methodList(), nil
	}
}

func methodNames() string {
	return strings.Join(methodList(), ", ")
}

func methodList() []string {
	names := make([]string, 0, len(githubstack.Methods))
	for _, method := range githubstack.Methods {
		names = append(names, string(method))
	}
	return names
}
