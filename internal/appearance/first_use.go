package appearance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Version 2 is used only for an originally absent desktop table. Old Helpers
// reject it rather than silently consuming recovery metadata they cannot honor.
// Version 1 backups and existing-table behavior remain supported unchanged.
const absentDesktopBackupVersion = 2

type absentDesktopBackup struct {
	OriginalSHA256 string `json:"originalSha256"`
	Separator      string `json:"separator"`
	Newline        string `json:"newline"`
}

func configNewline(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func configDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func newAbsentDesktopBackup(content string) *absentDesktopBackup {
	metadata := &absentDesktopBackup{OriginalSHA256: configDigest(content), Newline: configNewline(content)}
	if content != "" && !strings.HasSuffix(content, "\n") {
		metadata.Separator = metadata.Newline
	}
	return metadata
}

func validBackupStructure(stored backup) bool {
	if stored.SchemaVersion == backupSchemaVersion {
		return stored.AbsentDesktop == nil
	}
	metadata := stored.AbsentDesktop
	if stored.SchemaVersion != absentDesktopBackupVersion || metadata == nil ||
		(metadata.Newline != "\n" && metadata.Newline != "\r\n") ||
		(metadata.Separator != "" && metadata.Separator != metadata.Newline) {
		return false
	}
	digest, err := hex.DecodeString(metadata.OriginalSHA256)
	if err != nil || len(digest) != sha256.Size || strings.ToLower(metadata.OriginalSHA256) != metadata.OriginalSHA256 {
		return false
	}
	for _, key := range managedKeys {
		if stored.Values[key] != nil {
			return false
		}
	}
	return true
}

func restoreAbsentDesktop(content string, metadata *absentDesktopBackup) (string, error) {
	section, err := desktopSection(content)
	if err != nil || section == nil {
		return content, err
	}
	// Managed keys have already been restored. A new setting or comment belongs
	// to the user/app: retain its table, never delete or move it into another one.
	if strings.TrimSpace(section.body) != "" {
		return content, nil
	}
	headerLine := strings.TrimRight(content[section.start:section.bodyStart], "\r\n")
	if stripComment(headerLine) != headerLine {
		return content, nil
	}
	prefix := content[:section.start]
	// Remove only the separating newline we can prove was appended to the
	// exact original prefix. If other content changed, preserve its formatting.
	if metadata.Separator != "" && section.body == "" && section.bodyEnd == len(content) && strings.HasSuffix(prefix, metadata.Separator) {
		original := strings.TrimSuffix(prefix, metadata.Separator)
		if configDigest(original) == metadata.OriginalSHA256 {
			prefix = original
		}
	}
	return prefix + section.body + content[section.bodyEnd:], nil
}

// An inline/dotted root desktop key already defines this table even without a
// [desktop] header. Refuse to append a conflicting TOML table in that case.
func validateDesktopCreation(content string) error {
	headers, err := tableHeaders(content)
	if err != nil {
		return err
	}
	rootEnd := len(content)
	if len(headers) > 0 {
		rootEnd = headers[0].index
	}
	for _, line := range splitLines(content[:rootEnd]) {
		line = strings.TrimSpace(stripComment(line))
		key, _, assignment := strings.Cut(line, "=")
		if !assignment {
			continue
		}
		first, _, err := firstTOMLKey(key)
		if err != nil {
			return err
		}
		if first == "desktop" {
			return fmt.Errorf("inline or dotted desktop settings cannot be safely extended")
		}
	}
	return nil
}

// Parse only a table/root key's first component, without evaluating values or
// collecting unrelated settings. Quoted aliases must not hide an existing
// desktop table from either creation or restore.
func firstTOMLKey(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", fmt.Errorf("empty TOML key")
	}
	if value[0] == '"' || value[0] == '\'' {
		quote := value[0]
		escaped := false
		for index := 1; index < len(value); index++ {
			if quote == '"' && !escaped && value[index] == '\\' {
				if index+1 >= len(value) || !strings.ContainsRune("btnfr\"\\uU", rune(value[index+1])) {
					return "", "", fmt.Errorf("unsupported TOML key escape")
				}
				escaped = true
				continue
			}
			if value[index] == quote && !escaped {
				key := value[1:index]
				if quote == '"' {
					var err error
					key, err = strconv.Unquote(value[:index+1])
					if err != nil {
						return "", "", fmt.Errorf("unsupported quoted TOML key")
					}
				}
				return key, strings.TrimSpace(value[index+1:]), nil
			}
			escaped = false
		}
		return "", "", fmt.Errorf("unterminated TOML key")
	}
	end := 0
	for end < len(value) {
		character := value[end]
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '_' || character == '-') {
			break
		}
		end++
	}
	if end == 0 {
		return "", "", fmt.Errorf("unsupported TOML key")
	}
	return value[:end], strings.TrimSpace(value[end:]), nil
}
