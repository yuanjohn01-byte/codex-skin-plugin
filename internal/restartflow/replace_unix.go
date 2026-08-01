//go:build !windows

package restartflow

import (
	"fmt"
	"os"
)

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return ErrUnsafe
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("%w: sync directory", ErrUnsafe)
	}
	return nil
}
