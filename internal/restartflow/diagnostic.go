package restartflow

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restarttrace"
)

// WriteDiagnostic replaces one bounded local report; it is not continuation
// state and cannot authorize a restart. Bind it to the current request under the
// existing lock so an old worker cannot overwrite a newer request's report.
func (store *Store) WriteDiagnostic(report restarttrace.Report) error {
	if store == nil || !requestIDPattern.MatchString(report.RequestID) || report.SchemaVersion != 1 || len(report.Events) > restarttrace.MaxEntries {
		return ErrUnsafe
	}
	raw, err := json.Marshal(report)
	if err != nil || len(raw) > 16*1024 {
		return ErrUnsafe
	}
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, found, err := store.readCurrent()
	if err != nil || !found || current.RequestID != report.RequestID {
		return ErrState
	}
	path := filepath.Join(store.directory, "diagnostic.json")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafe
		}
	} else if !os.IsNotExist(err) {
		return ErrUnsafe
	}
	temporary, err := os.CreateTemp(store.directory, ".restart-diagnostic-*.tmp")
	if err != nil {
		return ErrUnsafe
	}
	defer os.Remove(temporary.Name())
	defer temporary.Close()
	if err := temporary.Chmod(0o600); err != nil {
		return ErrUnsafe
	}
	if _, err := temporary.Write(append(raw, '\n')); err != nil {
		return ErrUnsafe
	}
	if err := temporary.Sync(); err != nil {
		return ErrUnsafe
	}
	if err := temporary.Close(); err != nil {
		return ErrUnsafe
	}
	if err := replaceFile(temporary.Name(), path); err != nil {
		return ErrUnsafe
	}
	return syncDirectory(store.directory)
}
