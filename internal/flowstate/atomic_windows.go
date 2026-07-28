//go:build windows

package flowstate

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32FlowState     = syscall.NewLazyDLL("kernel32.dll")
	replaceFileWFlowState = kernel32FlowState.NewProc("ReplaceFileW")
	moveFileExWFlowState  = kernel32FlowState.NewProc("MoveFileExW")
)

const (
	replaceFileWriteThroughFlowState = 0x00000002
	moveFileReplaceExistingFlowState = 0x00000001
	moveFileWriteThroughFlowState    = 0x00000008
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
	if _, statErr := os.Lstat(destination); statErr == nil {
		result, _, callErr := replaceFileWFlowState.Call(
			uintptr(unsafe.Pointer(destinationPointer)),
			uintptr(unsafe.Pointer(sourcePointer)),
			0,
			replaceFileWriteThroughFlowState,
			0,
			0,
		)
		if result == 0 {
			return callErr
		}
		return nil
	}
	result, _, callErr := moveFileExWFlowState.Call(
		uintptr(unsafe.Pointer(sourcePointer)),
		uintptr(unsafe.Pointer(destinationPointer)),
		moveFileReplaceExistingFlowState|moveFileWriteThroughFlowState,
	)
	if result == 0 {
		return callErr
	}
	return nil
}
