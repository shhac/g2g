package main

import (
	"fmt"
	"os"

	"github.com/shhac/gt2gh/internal/cli"
)

// version is overridden by release builds with -ldflags.
var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}
