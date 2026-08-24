//go:build windows

package restartflow

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

var (
	restartKernel32     = syscall.NewLazyDLL("kernel32.dll")
	restartMoveFileExW  = restartKernel32.NewProc("MoveFileExW")
	restartReplaceFlags = uintptr(0x1 | 0x8)
)

func replaceFile(source, destination string) error {
	sourcePointer, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := restartMoveFileExW.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(destinationPointer)),
		restartReplaceFlags,
	)
	if result == 0 {
		if callErr != nil && !errors.Is(callErr, syscall.Errno(0)) {
			return callErr
		}
		return os.ErrInvalid
	}
	return nil
}

func syncDirectory(string) error {
	return nil
}
