package main

import (
	"os"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/guardiancli"
)

func main() {
	os.Exit(guardiancli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
