//go:build darwin

package browseropen

import (
	"context"
	"os/exec"
)

func openPlatform(ctx context.Context, value string) error {
	return exec.CommandContext(ctx, "/usr/bin/open", value).Run()
}
