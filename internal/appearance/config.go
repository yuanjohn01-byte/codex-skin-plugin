// Package appearance synchronizes Codex's native light/dark preference with a
// data-only Codex Skin theme and restores the exact pre-install setting.
package appearance

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const backupSchemaVersion = 1

var managedKeys = []string{"appearanceTheme", "appearanceDarkCodeThemeId"}

type Manager struct {
	configPath string
	backupPath string
	platform   string
}

type backup struct {
	SchemaVersion int                `json:"schemaVersion"`
	Platform      string             `json:"platform"`
	ConfigPath    string             `json:"configPath"`
	Values        map[string]*string `json:"values"`
}

type section struct {
	bodyStart int
	bodyEnd   int
	body      string
}

type header struct {
	index     int
	bodyStart int
	desktop   bool
}

func New(configPath, backupPath, platform string) (*Manager, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil || configPath != filepath.Clean(configPath) {
		return nil, fmt.Errorf("unsafe Codex config path")
	}
	backupPath, err = filepath.Abs(backupPath)
	if err != nil || backupPath != filepath.Clean(backupPath) || platform == "" {
		return nil, fmt.Errorf("unsafe appearance backup path")
	}
	return &Manager{configPath: configPath, backupPath: backupPath, platform: platform}, nil
}

func (manager *Manager) NeedsPin(mode string) (bool, error) {
	if mode != "dark" && mode != "light" {
		return false, fmt.Errorf("unsupported appearance mode")
	}
	_, found, err := manager.readBackup()
	if err != nil {
		return false, err
	}
	if !found {
		// Even when Codex already uses the requested appearance, first apply must
		// record that exact pre-theme state so offline Restore can remove only the
		// settings Codex Skin owns.
		return true, nil
	}
	content, _, err := manager.readConfig()
	if err != nil {
		return false, err
	}
	current, err := settingValue(content, "appearanceTheme")
	if err != nil {
		return false, err
	}
	return current == nil || *current != `"`+mode+`"`, nil
}

func (manager *Manager) NeedsRestore() (bool, error) {
	_, found, err := manager.readBackup()
	if err != nil || !found {
		return false, err
	}
	// A retained backup must be consumed even when the visible values already
	// match, otherwise a later install could restore stale pre-install state.
	return true, nil
}

func (manager *Manager) Pin(mode string) (bool, error) {
	if mode != "dark" && mode != "light" {
		return false, fmt.Errorf("unsupported appearance mode")
	}
	release, err := manager.lock()
	if err != nil {
		return false, err
	}
	defer release()

	content, info, err := manager.readConfig()
	if err != nil {
		return false, err
	}
	backupCreated := false
	if _, found, err := manager.readBackup(); err != nil {
		return false, err
	} else if !found {
		values := map[string]*string{}
		for _, key := range managedKeys {
			line, err := settingLine(content, key)
			if err != nil {
				return false, err
			}
			values[key] = line
		}
		if err := manager.writeBackup(backup{
			SchemaVersion: backupSchemaVersion,
			Platform:      manager.platform, ConfigPath: manager.configPath, Values: values,
		}); err != nil {
			return false, err
		}
		backupCreated = true
	}
	current, err := settingValue(content, "appearanceTheme")
	if err != nil {
		return false, err
	}
	if current != nil && *current == `"`+mode+`"` {
		return backupCreated, nil
	}
	updated, err := replaceSetting(content, "appearanceTheme", pointer(`appearanceTheme = "`+mode+`"`))
	if err != nil {
		return false, err
	}
	if updated == content {
		return backupCreated, nil
	}
	return true, atomicReplace(manager.configPath, []byte(content), []byte(updated), info)
}

func (manager *Manager) Restore() (bool, error) {
	release, err := manager.lock()
	if err != nil {
		return false, err
	}
	defer release()

	stored, found, err := manager.readBackup()
	if err != nil || !found {
		return false, err
	}
	content, info, err := manager.readConfig()
	if err != nil {
		return false, err
	}
	updated := content
	for _, key := range managedKeys {
		updated, err = replaceSetting(updated, key, stored.Values[key])
		if err != nil {
			return false, err
		}
	}
	changed := updated != content
	if changed {
		if err := atomicReplace(manager.configPath, []byte(content), []byte(updated), info); err != nil {
			return false, err
		}
	}
	if err := rejectSymlink(manager.backupPath); err != nil {
		return false, err
	}
	if err := os.Remove(manager.backupPath); err != nil {
		return false, err
	}
	return changed, syncDirectory(filepath.Dir(manager.backupPath))
}

func (manager *Manager) readConfig() (string, os.FileInfo, error) {
	info, err := os.Lstat(manager.configPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("Codex config must be a regular file")
	}
	raw, err := os.ReadFile(manager.configPath)
	if err != nil || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return "", nil, fmt.Errorf("Codex config is not strict UTF-8")
	}
	content := string(raw)
	if strings.Contains(content, `"""`) || strings.Contains(content, `'''`) {
		return "", nil, fmt.Errorf("multiline TOML strings are not rewritten")
	}
	if _, err := desktopSection(content); err != nil {
		return "", nil, err
	}
	return content, info, nil
}

func (manager *Manager) readBackup() (backup, bool, error) {
	if err := rejectSymlink(manager.backupPath); err != nil {
		return backup{}, false, err
	}
	raw, err := os.ReadFile(manager.backupPath)
	if errors.Is(err, os.ErrNotExist) {
		return backup{}, false, nil
	}
	if err != nil || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return backup{}, false, fmt.Errorf("appearance backup is unreadable")
	}
	var stored backup
	if json.Unmarshal(raw, &stored) != nil ||
		stored.SchemaVersion != backupSchemaVersion ||
		stored.Platform != manager.platform ||
		stored.ConfigPath != manager.configPath ||
		len(stored.Values) != len(managedKeys) {
		return backup{}, false, fmt.Errorf("appearance backup identity mismatch")
	}
	for _, key := range managedKeys {
		line, ok := stored.Values[key]
		if !ok || line != nil && !validBackupLine(key, *line) {
			return backup{}, false, fmt.Errorf("appearance backup setting mismatch")
		}
	}
	return stored, true, nil
}

func (manager *Manager) writeBackup(stored backup) error {
	if err := rejectSymlink(manager.backupPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manager.backupPath), 0o700); err != nil {
		return err
	}
	content, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	return atomicCreate(manager.backupPath, content, 0o600)
}

func (manager *Manager) lock() (func(), error) {
	path := manager.configPath + ".codex-skin.lock"
	if err := rejectSymlink(path); err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return nil, fmt.Errorf("another appearance operation is active")
	}
	return func() { _ = os.Remove(path) }, nil
}

func settingValue(content, key string) (*string, error) {
	line, err := settingLine(content, key)
	if err != nil || line == nil {
		return nil, err
	}
	_, value, ok := strings.Cut(*line, "=")
	if !ok {
		return nil, fmt.Errorf("invalid setting")
	}
	value = strings.TrimSpace(stripComment(value))
	return &value, nil
}

func settingLine(content, key string) (*string, error) {
	section, err := desktopSection(content)
	if err != nil || section == nil {
		return nil, err
	}
	var found *string
	for _, line := range splitLines(section.body) {
		structure := strings.TrimSpace(stripComment(strings.TrimRight(line, "\r\n")))
		name, _, ok := strings.Cut(structure, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("duplicate %s setting", key)
		}
		value := strings.TrimRight(line, "\r\n")
		found = &value
	}
	return found, nil
}

func replaceSetting(content, key string, line *string) (string, error) {
	section, err := desktopSection(content)
	if err != nil {
		return "", err
	}
	if section == nil {
		content = strings.TrimRight(content, "\r\n") + "\n\n[desktop]\n"
		section, err = desktopSection(content)
		if err != nil || section == nil {
			return "", fmt.Errorf("could not create desktop table")
		}
	}
	newline := "\n"
	if strings.Contains(section.body, "\r\n") {
		newline = "\r\n"
	}
	lines := splitLines(section.body)
	result := strings.Builder{}
	found := false
	for _, existing := range lines {
		structure := strings.TrimSpace(stripComment(strings.TrimRight(existing, "\r\n")))
		name, _, ok := strings.Cut(structure, "=")
		if !ok || strings.TrimSpace(name) != key {
			result.WriteString(existing)
			continue
		}
		if found {
			return "", fmt.Errorf("duplicate %s setting", key)
		}
		found = true
		if line != nil {
			result.WriteString(*line)
			result.WriteString(newline)
		}
	}
	if !found && line != nil {
		if result.Len() > 0 && !strings.HasSuffix(result.String(), "\n") {
			result.WriteString(newline)
		}
		result.WriteString(*line)
		result.WriteString(newline)
	}
	return content[:section.bodyStart] + result.String() + content[section.bodyEnd:], nil
}

func desktopSection(content string) (*section, error) {
	headers, err := tableHeaders(content)
	if err != nil {
		return nil, err
	}
	var position = -1
	for index, item := range headers {
		if item.desktop {
			if position >= 0 {
				return nil, fmt.Errorf("multiple desktop tables")
			}
			position = index
		}
	}
	if position < 0 {
		return nil, nil
	}
	item := headers[position]
	end := len(content)
	if position+1 < len(headers) {
		end = headers[position+1].index
	}
	return &section{bodyStart: item.bodyStart, bodyEnd: end, body: content[item.bodyStart:end]}, nil
}

func tableHeaders(content string) ([]header, error) {
	result := []header{}
	offset, depth := 0, 0
	for _, line := range splitLines(content) {
		structure := strings.TrimSpace(stripComment(strings.TrimRight(line, "\r\n")))
		if depth == 0 && isTableHeader(structure) {
			inside := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(structure, "["), "]"))
			result = append(result, header{
				index: offset, bodyStart: offset + len(line), desktop: inside == "desktop",
			})
		} else {
			expression := structure
			if depth == 0 {
				if _, after, ok := strings.Cut(structure, "="); ok {
					expression = after
				} else if strings.ContainsAny(structure, "[]") {
					return nil, fmt.Errorf("malformed TOML array syntax")
				}
			}
			for _, character := range expression {
				switch character {
				case '[':
					depth++
				case ']':
					depth--
					if depth < 0 {
						return nil, fmt.Errorf("unmatched TOML array bracket")
					}
				}
			}
		}
		offset += len(line)
	}
	if depth != 0 {
		return nil, fmt.Errorf("unterminated TOML array")
	}
	return result, nil
}

func stripComment(line string) string {
	quote, escaped := rune(0), false
	for index, character := range line {
		if quote == '"' {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == quote {
				quote = 0
			}
			continue
		}
		if quote == '\'' {
			if character == quote {
				quote = 0
			}
			continue
		}
		if character == '"' || character == '\'' {
			quote = character
		} else if character == '#' {
			return line[:index]
		}
	}
	return line
}

func isTableHeader(value string) bool {
	return strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") &&
		!strings.HasPrefix(value, "[[") && !strings.Contains(value[1:len(value)-1], "[")
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.SplitAfter(content, "\n")
	return lines
}

func atomicReplace(path string, expected, content []byte, info os.FileInfo) error {
	current, err := os.ReadFile(path)
	currentInfo, statErr := os.Lstat(path)
	if err != nil || statErr != nil || !bytes.Equal(current, expected) ||
		!currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(info, currentInfo) {
		return fmt.Errorf("Codex config changed during appearance update")
	}
	temporary, err := temporaryName(filepath.Dir(path), ".appearance-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, content, info.Mode().Perm()); err != nil {
		return err
	}
	// Windows requires a writable handle for FlushFileBuffers (os.File.Sync).
	// The temporary is private to this operation and is reopened only to make
	// its contents durable before the platform-specific atomic replacement.
	file, err := os.OpenFile(temporary, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = file.Sync()
	_ = file.Close()
	if err != nil {
		return err
	}
	if err := replaceFile(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func atomicCreate(path string, content []byte, mode os.FileMode) error {
	temporary, err := temporaryName(filepath.Dir(path), ".appearance-backup-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.WriteFile(temporary, content, mode); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func temporaryName(directory, prefix string) (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return filepath.Join(directory, prefix+hex.EncodeToString(raw)+".tmp"), nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe appearance state path")
	}
	return nil
}

func pointer(value string) *string { return &value }

func validBackupLine(key, line string) bool {
	if strings.ContainsAny(line, "\r\n\x00") {
		return false
	}
	name, _, ok := strings.Cut(stripComment(line), "=")
	return ok && strings.TrimSpace(name) == key
}
