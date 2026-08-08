//go:build windows

package appearance

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var appearanceMoveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func replaceFile(source, destination string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := appearanceMoveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(destinationPointer)),
		uintptr(moveFileReplaceExisting|moveFileWriteThrough),
	)
	if result == 0 {
		if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
			return callErr
		}
		return os.ErrInvalid
	}
	return nil
}

// Windows does not support opening a directory handle through os.Open for the
// fsync pattern used on Unix. MoveFileExW with MOVEFILE_WRITE_THROUGH provides
// the durability boundary for replacements; a successful hard-link creation
// is already visible atomically for backups.
func syncDirectory(string) error {
	return nil
}
