//go:build !darwin && !windows

package browseropen

import (
	"context"
	"errors"
)

func openPlatform(context.Context, string) error {
	return errors.New("browser launch is unsupported")
}
