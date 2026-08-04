//go:build !darwin && !windows

package restartflow

func StartWorker(string, string) error {
	return ErrUnsafe
}

func StartSession(string, string) error {
	return ErrUnsafe
}
