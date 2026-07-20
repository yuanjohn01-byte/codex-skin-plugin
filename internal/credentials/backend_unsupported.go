//go:build !darwin && !windows

package credentials

func newBackend() (backend, error) {
	return nil, ErrUnsupported
}
