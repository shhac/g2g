package main

import (
	"os"
	"path/filepath"

	"github.com/shhac/gt2gh/internal/cli"
)

// version is overridden by release builds with -ldflags.
var version = "dev"

func main() {
	cli.Execute(version, filepath.Base(os.Args[0]))
}
