package main

import (
	"os"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr, cli.Runtime{}))
}
