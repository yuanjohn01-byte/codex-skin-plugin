package appearance

import (
	"errors"
	"fmt"
	"reflect"
)

// ErrLiveRestoreUnavailable means the fixed native mode control cannot restore
// this backup on its own. The caller may use the existing consented reload.
var ErrLiveRestoreUnavailable = errors.New("appearance backup requires ordinary restore")

// ErrNoLiveRestorePoint is a no-op, unlike an unsupported existing backup.
var ErrNoLiveRestorePoint = errors.New("no appearance recovery point")

// LiveRestore holds the same lock as LiveSwitch, but never creates a new backup.
// Stage restores exact disk formatting only after Codex has persisted the target
// mode; Commit consumes recovery only after the caller verifies the live result.
type LiveRestore struct {
	*LiveSwitch
	stored backup
	mode   string
	staged bool
}

func (manager *Manager) BeginLiveRestore(expectedMode string) (*LiveRestore, error) {
	if !validMode(expectedMode) {
		return nil, fmt.Errorf("unsupported live appearance mode")
	}
	release, err := manager.lock()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()
	stored, found, err := manager.readBackup()
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoLiveRestorePoint
	}
	content, _, err := manager.readConfig()
	if err != nil {
		return nil, err
	}
	currentMode, err := appearanceMode(content)
	if err != nil || currentMode != expectedMode {
		return nil, fmt.Errorf("Codex config and live appearance differ")
	}
	mode := "system"
	value, err := backupValue(stored.Values["appearanceTheme"])
	if err != nil {
		return nil, err
	}
	if value != nil {
		switch *value {
		case `"system"`:
		case `"light"`:
			mode = "light"
		case `"dark"`:
			mode = "dark"
		default:
			return nil, ErrLiveRestoreUnavailable
		}
	}
	transaction := &LiveRestore{
		LiveSwitch: &LiveSwitch{manager: manager, release: release},
		stored:     stored, mode: mode,
	}
	if err := transaction.otherSettingsMatch(content); err != nil {
		return nil, err
	}
	failed = false
	return transaction, nil
}

func (transaction *LiveRestore) Mode() string { return transaction.mode }

func (transaction *LiveRestore) checkBackup() error {
	if transaction == nil || transaction.LiveSwitch == nil || transaction.closed {
		return fmt.Errorf("appearance transaction is closed")
	}
	stored, found, err := transaction.manager.readBackup()
	if err != nil || !found || !reflect.DeepEqual(stored, transaction.stored) {
		return fmt.Errorf("appearance recovery point changed")
	}
	return nil
}

func (transaction *LiveRestore) otherSettingsMatch(content string) error {
	for _, key := range managedKeys {
		if key == "appearanceTheme" {
			continue
		}
		current, err := settingValue(content, key)
		if err != nil {
			return err
		}
		original, err := backupValue(transaction.stored.Values[key])
		if err != nil {
			return err
		}
		if !sameOptionalValue(current, original) {
			return ErrLiveRestoreUnavailable
		}
	}
	return nil
}

func (transaction *LiveRestore) Stage() error {
	if err := transaction.checkBackup(); err != nil {
		return err
	}
	if err := transaction.VerifyMode(transaction.mode); err != nil {
		return err
	}
	content, _, err := transaction.manager.readConfig()
	if err != nil {
		return err
	}
	if err := transaction.otherSettingsMatch(content); err != nil {
		return err
	}
	if _, err := transaction.manager.restoreConfig(transaction.stored); err != nil {
		return err
	}
	transaction.staged = true
	return transaction.VerifyRestored()
}

func (transaction *LiveRestore) VerifyRestored() error {
	if err := transaction.checkBackup(); err != nil {
		return err
	}
	if !transaction.staged {
		return fmt.Errorf("appearance restore is not staged")
	}
	content, _, err := transaction.manager.readConfig()
	if err != nil {
		return err
	}
	for _, key := range managedKeys {
		current, err := settingLine(content, key)
		if err != nil || !sameOptionalValue(current, transaction.stored.Values[key]) {
			return fmt.Errorf("appearance restore did not preserve saved settings")
		}
	}
	return transaction.VerifyMode(transaction.mode)
}

func (transaction *LiveRestore) Commit() error {
	if err := transaction.VerifyRestored(); err != nil {
		return err
	}
	if err := transaction.manager.consumeBackup(); err != nil {
		return err
	}
	transaction.Close()
	return nil
}
