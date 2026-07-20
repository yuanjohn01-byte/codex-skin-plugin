//go:build windows

package credentials

import (
	"context"
	"errors"
	"runtime"
	"syscall"
	"unsafe"
)

const (
	credentialTypeGeneric         = 1
	credentialPersistLocalMachine = 2
	errorNotFound                 = syscall.Errno(1168)
)

var (
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	credentialWriteW  = advapi32.NewProc("CredWriteW")
	credentialReadW   = advapi32.NewProc("CredReadW")
	credentialDeleteW = advapi32.NewProc("CredDeleteW")
	credentialFree    = advapi32.NewProc("CredFree")
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        syscall.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsBackend struct{}

func newBackend() (backend, error) {
	return &windowsBackend{}, nil
}

func (backend *windowsBackend) put(ctx context.Context, deviceID string, secret []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(windowsTargetPrefix + deviceID)
	if err != nil {
		return ErrInvalidDeviceID
	}
	username, err := syscall.UTF16PtrFromString(deviceID)
	if err != nil {
		return ErrInvalidDeviceID
	}
	credential := windowsCredential{
		Type:               credentialTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(secret)),
		CredentialBlob:     &secret[0],
		Persist:            credentialPersistLocalMachine,
		UserName:           username,
	}
	result, _, callErr := credentialWriteW.Call(
		uintptr(unsafe.Pointer(&credential)),
		0,
	)
	runtime.KeepAlive(secret)
	runtime.KeepAlive(target)
	runtime.KeepAlive(username)
	if result == 0 {
		return windowsError(callErr)
	}
	return nil
}

func (backend *windowsBackend) get(ctx context.Context, deviceID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := syscall.UTF16PtrFromString(windowsTargetPrefix + deviceID)
	if err != nil {
		return nil, ErrInvalidDeviceID
	}
	var rawCredential uintptr
	result, _, callErr := credentialReadW.Call(
		uintptr(unsafe.Pointer(target)),
		credentialTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&rawCredential)),
	)
	runtime.KeepAlive(target)
	if result == 0 {
		return nil, windowsError(callErr)
	}
	defer credentialFree.Call(rawCredential)
	credential := (*windowsCredential)(unsafe.Pointer(rawCredential))
	if credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil || credential.CredentialBlobSize > maximumSecretBytes {
		return nil, ErrInvalidSecret
	}
	blob := unsafe.Slice(credential.CredentialBlob, int(credential.CredentialBlobSize))
	secret := append([]byte(nil), blob...)
	zeroBytes(blob)
	runtime.KeepAlive(credential)
	return secret, nil
}

func (backend *windowsBackend) delete(ctx context.Context, deviceID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := syscall.UTF16PtrFromString(windowsTargetPrefix + deviceID)
	if err != nil {
		return ErrInvalidDeviceID
	}
	result, _, callErr := credentialDeleteW.Call(
		uintptr(unsafe.Pointer(target)),
		credentialTypeGeneric,
		0,
	)
	runtime.KeepAlive(target)
	if result == 0 {
		return windowsError(callErr)
	}
	return nil
}

func windowsError(err error) error {
	if errors.Is(err, errorNotFound) {
		return ErrNotFound
	}
	return ErrUnavailable
}
