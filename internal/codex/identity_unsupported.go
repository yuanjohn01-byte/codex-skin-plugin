//go:build !darwin && !windows

package codex

import "context"

func DiscoverInstallation(context.Context) (Installation, error) {
	return Installation{}, ErrIdentityUntrusted
}

func probeStableInstallation(context.Context, Installation) (Installation, error) {
	return Installation{}, ErrIdentityUntrusted
}

func LaunchControlled(context.Context, Installation, string, int) (int, error) {
	return 0, ErrLaunchFailed
}

func LaunchOrdinary(context.Context, Installation) error {
	return ErrLaunchFailed
}

func VerifyListener(context.Context, Installation, int, int, string) (ProcessIdentity, error) {
	return ProcessIdentity{}, ErrListenerUntrusted
}

func VerifyProcess(context.Context, Installation, int, int, string) (ProcessIdentity, error) {
	return ProcessIdentity{}, ErrListenerUntrusted
}

func StopOwnedProcess(context.Context, Installation, ProcessIdentity, int, string) error {
	return ErrListenerUntrusted
}

func DefaultUserProfile(Installation) (string, error) {
	return "", ErrCurrentUnsafe
}

func DiscoverCurrentInstance(context.Context, Installation) (CurrentInstance, error) {
	return CurrentInstance{}, ErrCurrentMissing
}

func StopCurrentInstance(context.Context, Installation, CurrentInstance) error {
	return ErrCurrentUnsafe
}
