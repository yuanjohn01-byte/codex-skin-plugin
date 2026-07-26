//go:build windows

package engine

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockFileExclusiveLock   = 0x00000002
	lockFileFailImmediately = 0x00000001
	errorLockViolation      = syscall.Errno(33)
)

var (
	lockFileExProc   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	unlockFileExProc = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

func acquireFileLock(path string) (func() error, error) {
	if err := rejectSymlinkIfPresent(path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: open lock: %v", ErrStateUnsafe, err)
	}
	var overlapped syscall.Overlapped
	result, _, callErr := lockFileExProc.Call(
		file.Fd(),
		uintptr(lockFileExclusiveLock|lockFileFailImmediately),
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if result == 0 {
		file.Close()
		if errors.Is(callErr, errorLockViolation) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("%w: acquire lock: %v", ErrStateUnsafe, callErr)
	}
	return func() error {
		unlockResult, _, unlockErr := unlockFileExProc.Call(
			file.Fd(),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(&overlapped)),
		)
		closeErr := file.Close()
		if unlockResult == 0 {
			return errors.Join(unlockErr, closeErr)
		}
		return closeErr
	}, nil
}
