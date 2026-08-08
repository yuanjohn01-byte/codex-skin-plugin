package main

import (
	"os"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/bootstrapcli"
)

func main() {
	os.Exit(bootstrapcli.Run(os.Args[1:], os.Stdout, os.Stderr, bootstrapcli.Runtime{}))
}
