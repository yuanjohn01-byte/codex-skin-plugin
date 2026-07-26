//go:build !darwin && !windows

package codex

import "context"

func DiscoverInstallation(context.Context) (Installation, error) {
	return Installation{}, ErrIdentityUntrusted
}

func LaunchControlled(context.Context, Installation, string, int) (int, error) {
	return 0, ErrLaunchFailed
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
