package engine

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	maxStateFileBytes     = 256 * 1024
	maxRecoveryStyleBytes = 256 * 1024
	maxRecoveryBackground = 24 * 1024 * 1024
)

var (
	storedThemePublicID = regexp.MustCompile(`^[0-9]{6}$`)
	storedThemeVersion  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?$`)
	storedDigest        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Store struct {
	root string
}

type Journal struct {
	SchemaVersion int    `json:"schemaVersion"`
	OperationID   string `json:"operationId"`
	Kind          string `json:"kind"`
	Stage         string `json:"stage"`
	Status        string `json:"status"`
	ThemePublicID string `json:"themePublicId,omitempty"`
	ThemeVersion  string `json:"themeVersion,omitempty"`
	StartedAt     string `json:"startedAt"`
	UpdatedAt     string `json:"updatedAt"`
	ErrorCode     string `json:"errorCode,omitempty"`
	RecoveryID    string `json:"recoveryId,omitempty"`
}

type RecoveryPoint struct {
	SchemaVersion      int           `json:"schemaVersion"`
	RecoveryID         string        `json:"recoveryId"`
	OperationID        string        `json:"operationId"`
	CapturedAt         string        `json:"capturedAt"`
	WasThemed          bool          `json:"wasThemed"`
	ThemePublicID      string        `json:"themePublicId,omitempty"`
	ThemeVersion       string        `json:"themeVersion,omitempty"`
	TemplateVersion    int           `json:"templateVersion,omitempty"`
	StyleByteSize      int           `json:"styleByteSize,omitempty"`
	StyleSHA256        string        `json:"styleSha256,omitempty"`
	BackgroundByteSize int           `json:"backgroundByteSize,omitempty"`
	BackgroundSHA256   string        `json:"backgroundSha256,omitempty"`
	PreviousDesired    *DesiredTheme `json:"previousDesired,omitempty"`
}

type LastKnownGood struct {
	SchemaVersion int    `json:"schemaVersion"`
	RecoveryID    string `json:"recoveryId"`
	UpdatedAt     string `json:"updatedAt"`
}

type DesiredTheme struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ThemePublicID   string `json:"themePublicId"`
	ThemeVersion    string `json:"themeVersion"`
	PackageSHA256   string `json:"packageSha256"`
	TemplateVersion int    `json:"templateVersion"`
	AppliedAt       string `json:"appliedAt"`
}

func DefaultRoot(goos, home, localAppData string) (string, error) {
	switch goos {
	case "darwin":
		if home == "" {
			return "", fmt.Errorf("%w: home directory is empty", ErrStateUnsafe)
		}
		return filepath.Join(home, "Library", "Application Support", "CodexSkin"), nil
	case "windows":
		if localAppData == "" {
			return "", fmt.Errorf("%w: LOCALAPPDATA is empty", ErrStateUnsafe)
		}
		separator := `\`
		if strings.Contains(localAppData, "/") && !strings.Contains(localAppData, `\`) {
			separator = "/"
		}
		return strings.TrimRight(localAppData, `\/`) + separator + "CodexSkin", nil
	default:
		return "", fmt.Errorf("%w: unsupported platform", ErrStateUnsafe)
	}
}

func OpenStore(root, pluginCache string) (*Store, error) {
	secureRoot, err := secureAbsolute(root)
	if err != nil {
		return nil, err
	}
	if pluginCache != "" {
		secureCache, err := secureAbsolute(pluginCache)
		if err != nil {
			return nil, err
		}
		if pathsOverlap(secureRoot, secureCache) {
			return nil, fmt.Errorf("%w: state root overlaps Plugin cache", ErrStateUnsafe)
		}
	}
	for _, directory := range []string{
		secureRoot,
		filepath.Join(secureRoot, "state"),
		filepath.Join(secureRoot, "state", "operations"),
		filepath.Join(secureRoot, "themes"),
		filepath.Join(secureRoot, "recovery"),
		filepath.Join(secureRoot, "recovery", "points"),
		filepath.Join(secureRoot, "tmp"),
	} {
		if err := ensureSecureDirectory(directory); err != nil {
			return nil, err
		}
	}
	return &Store{root: secureRoot}, nil
}

func (store *Store) Root() string {
	return store.root
}

func (store *Store) Lock() (func() error, error) {
	return acquireFileLock(filepath.Join(store.root, "state", "operation.lock"))
}

func (store *Store) NewOperationID() (string, error) {
	return randomID("op_", 16)
}

func (store *Store) NewRecoveryID() (string, error) {
	return randomID("rec_", 16)
}

func (store *Store) WriteJournal(journal Journal) error {
	journal.SchemaVersion = StateSchemaVersion
	journal.UpdatedAt = canonicalNow()
	if journal.StartedAt == "" {
		journal.StartedAt = journal.UpdatedAt
	}
	if !safeIdentifier(journal.OperationID, "op_") {
		return fmt.Errorf("%w: invalid operation id", ErrStateUnsafe)
	}
	return writeJSONAtomic(filepath.Join(store.root, "state", "operations", journal.OperationID+".json"), journal)
}

func (store *Store) RunningJournals() ([]Journal, error) {
	directory := filepath.Join(store.root, "state", "operations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("%w: list operation journals: %v", ErrStateUnsafe, err)
	}
	journals := make([]Journal, 0)
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() ||
			!strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("%w: operation journal entry", ErrStateUnsafe)
		}
		var journal Journal
		found, err := readJSON(filepath.Join(directory, entry.Name()), &journal)
		if err != nil {
			return nil, err
		}
		if !found || journal.SchemaVersion != StateSchemaVersion ||
			!safeIdentifier(journal.OperationID, "op_") ||
			entry.Name() != journal.OperationID+".json" {
			return nil, fmt.Errorf("%w: operation journal identity", ErrStateUnsafe)
		}
		if journal.Status == "running" {
			journals = append(journals, journal)
		}
	}
	sort.Slice(journals, func(left, right int) bool {
		if journals[left].StartedAt == journals[right].StartedAt {
			return journals[left].OperationID < journals[right].OperationID
		}
		return journals[left].StartedAt < journals[right].StartedAt
	})
	return journals, nil
}

func (store *Store) WriteRecoveryPoint(point RecoveryPoint, snapshot Snapshot) error {
	point.SchemaVersion = StateSchemaVersion
	if !safeIdentifier(point.RecoveryID, "rec_") || !safeIdentifier(point.OperationID, "op_") {
		return fmt.Errorf("%w: invalid recovery identity", ErrStateUnsafe)
	}
	if point.WasThemed != snapshot.StylePresent {
		return fmt.Errorf("%w: recovery theme state mismatch", ErrStateUnsafe)
	}
	if point.PreviousDesired != nil && !validDesired(*point.PreviousDesired) {
		return fmt.Errorf("%w: invalid previous desired theme", ErrStateUnsafe)
	}
	directory := filepath.Join(store.root, "recovery", "points", point.RecoveryID)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("%w: create recovery point: %v", ErrStateUnsafe, err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(directory)
		}
	}()
	if snapshot.StylePresent {
		if !validSnapshot(snapshot) {
			return fmt.Errorf("%w: invalid recovery snapshot", ErrStateUnsafe)
		}
		style := []byte(snapshot.StyleText)
		background := []byte(snapshot.BackgroundDataURL)
		point.ThemePublicID = snapshot.ThemePublicID
		point.ThemeVersion = snapshot.ThemeVersion
		point.TemplateVersion = snapshot.TemplateVersion
		point.StyleByteSize = len(style)
		point.StyleSHA256 = digestBytes(style)
		point.BackgroundByteSize = len(background)
		point.BackgroundSHA256 = digestBytes(background)
		if err := writeExclusiveFile(filepath.Join(directory, "style.css"), style); err != nil {
			return err
		}
		if err := writeExclusiveFile(filepath.Join(directory, "background.data"), background); err != nil {
			return err
		}
	}
	if err := writeJSONAtomic(filepath.Join(directory, "recovery.json"), point); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(store.root, "recovery", "last-known-good.json"), LastKnownGood{
		SchemaVersion: StateSchemaVersion,
		RecoveryID:    point.RecoveryID,
		UpdatedAt:     canonicalNow(),
	}); err != nil {
		return err
	}
	cleanup = false
	return syncDirectory(filepath.Dir(directory))
}

func (store *Store) ReadRecoveryPoint(recoveryID string) (RecoveryPoint, Snapshot, error) {
	if !safeIdentifier(recoveryID, "rec_") {
		return RecoveryPoint{}, Snapshot{}, fmt.Errorf("%w: invalid recovery identity", ErrStateUnsafe)
	}
	directory := filepath.Join(store.root, "recovery", "points", recoveryID)
	var point RecoveryPoint
	found, err := readJSON(filepath.Join(directory, "recovery.json"), &point)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("%w: recovery point is missing", ErrStateUnsafe)
		}
		return RecoveryPoint{}, Snapshot{}, err
	}
	if point.SchemaVersion != StateSchemaVersion ||
		point.RecoveryID != recoveryID ||
		!safeIdentifier(point.OperationID, "op_") {
		return RecoveryPoint{}, Snapshot{}, fmt.Errorf("%w: recovery point identity", ErrStateUnsafe)
	}
	if !point.WasThemed {
		return point, Snapshot{}, nil
	}
	style, err := readRegularStateFile(filepath.Join(directory, "style.css"), maxRecoveryStyleBytes)
	if err != nil {
		return RecoveryPoint{}, Snapshot{}, err
	}
	background, err := readRegularStateFile(filepath.Join(directory, "background.data"), maxRecoveryBackground)
	if err != nil {
		return RecoveryPoint{}, Snapshot{}, err
	}
	if len(style) != point.StyleByteSize ||
		digestBytes(style) != point.StyleSHA256 ||
		len(background) != point.BackgroundByteSize ||
		digestBytes(background) != point.BackgroundSHA256 {
		return RecoveryPoint{}, Snapshot{}, fmt.Errorf("%w: recovery point digest", ErrStateUnsafe)
	}
	snapshot := Snapshot{
		StylePresent: true, StyleText: string(style), BackgroundDataURL: string(background),
		ThemePublicID: point.ThemePublicID, ThemeVersion: point.ThemeVersion,
		TemplateVersion: point.TemplateVersion,
	}
	if !validSnapshot(snapshot) {
		return RecoveryPoint{}, Snapshot{}, fmt.Errorf("%w: invalid recovery snapshot", ErrStateUnsafe)
	}
	return point, snapshot, nil
}

func (store *Store) WriteDesired(desired DesiredTheme) error {
	desired.SchemaVersion = StateSchemaVersion
	if !validDesired(desired) {
		return fmt.Errorf("%w: invalid desired theme", ErrStateUnsafe)
	}
	return writeJSONAtomic(filepath.Join(store.root, "state", "desired-theme.json"), desired)
}

func (store *Store) ReadDesired() (DesiredTheme, bool, error) {
	var desired DesiredTheme
	found, err := readJSON(filepath.Join(store.root, "state", "desired-theme.json"), &desired)
	if err != nil || !found {
		return DesiredTheme{}, found, err
	}
	if desired.SchemaVersion != StateSchemaVersion || !validDesired(desired) {
		return DesiredTheme{}, false, fmt.Errorf("%w: desired theme schema", ErrStateUnsafe)
	}
	return desired, true, nil
}

func (store *Store) ClearDesired() error {
	path := filepath.Join(store.root, "state", "desired-theme.json")
	if err := rejectSymlinkIfPresent(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: clear desired theme: %v", ErrStateUnsafe, err)
	}
	return syncDirectory(filepath.Dir(path))
}

func (store *Store) StagingPath(operationID string) (string, error) {
	if !safeIdentifier(operationID, "op_") {
		return "", fmt.Errorf("%w: invalid operation id", ErrStateUnsafe)
	}
	return filepath.Join(store.root, "tmp", operationID), nil
}

func (store *Store) CommitTheme(staging string, desired DesiredTheme) (string, error) {
	if !validDesired(desired) {
		return "", fmt.Errorf("%w: invalid desired theme", ErrStateUnsafe)
	}
	expectedParent := filepath.Join(store.root, "tmp")
	if err := ensureContained(expectedParent, staging); err != nil {
		return "", err
	}
	destination := filepath.Join(store.root, "themes", desired.ThemePublicID, desired.ThemeVersion, desired.PackageSHA256)
	if err := ensureContained(filepath.Join(store.root, "themes"), destination); err != nil {
		return "", err
	}
	if err := ensureSecureDirectory(filepath.Dir(filepath.Dir(destination))); err != nil {
		return "", err
	}
	if err := ensureSecureDirectory(filepath.Dir(destination)); err != nil {
		return "", err
	}
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: cached theme path", ErrStateUnsafe)
		}
		return destination, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: inspect theme cache: %v", ErrStateUnsafe, err)
	}
	if err := os.Rename(staging, destination); err != nil {
		return "", fmt.Errorf("%w: commit theme cache: %v", ErrStateUnsafe, err)
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return "", fmt.Errorf("%w: sync theme cache: %v", ErrStateUnsafe, err)
	}
	return destination, nil
}

func (store *Store) ThemeCachePath(desired DesiredTheme) (string, error) {
	if !validDesired(desired) {
		return "", fmt.Errorf("%w: invalid desired theme", ErrStateUnsafe)
	}
	target := filepath.Join(store.root, "themes", desired.ThemePublicID, desired.ThemeVersion, desired.PackageSHA256)
	if err := ensureContained(filepath.Join(store.root, "themes"), target); err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil {
		return "", fmt.Errorf("%w: inspect cached theme: %v", ErrStateUnsafe, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: cached theme directory", ErrStateUnsafe)
	}
	return target, nil
}

func validDesired(desired DesiredTheme) bool {
	return storedThemePublicID.MatchString(desired.ThemePublicID) &&
		storedThemeVersion.MatchString(desired.ThemeVersion) &&
		storedDigest.MatchString(desired.PackageSHA256) &&
		desired.TemplateVersion == TemplateVersion &&
		desired.AppliedAt != ""
}

func validSnapshot(snapshot Snapshot) bool {
	return snapshot.StylePresent &&
		storedThemePublicID.MatchString(snapshot.ThemePublicID) &&
		storedThemeVersion.MatchString(snapshot.ThemeVersion) &&
		snapshot.TemplateVersion == TemplateVersion &&
		len(snapshot.StyleText) > 0 &&
		len(snapshot.StyleText) <= maxRecoveryStyleBytes &&
		len(snapshot.BackgroundDataURL) > 0 &&
		len(snapshot.BackgroundDataURL) <= maxRecoveryBackground &&
		(strings.HasPrefix(snapshot.BackgroundDataURL, "data:image/png;base64,") ||
			strings.HasPrefix(snapshot.BackgroundDataURL, "data:image/jpeg;base64,") ||
			strings.HasPrefix(snapshot.BackgroundDataURL, "data:image/webp;base64,"))
}

func digestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func writeExclusiveFile(target string, content []byte) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create recovery data: %v", ErrStateUnsafe, err)
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("%w: write recovery data", ErrStateUnsafe)
	}
	return nil
}

func readRegularStateFile(target string, limit int64) ([]byte, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect recovery data: %v", ErrStateUnsafe, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > limit {
		return nil, fmt.Errorf("%w: recovery data file shape", ErrStateUnsafe)
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("%w: open recovery data: %v", ErrStateUnsafe, err)
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(content)) != info.Size() {
		return nil, fmt.Errorf("%w: read recovery data", ErrStateUnsafe)
	}
	return content, nil
}

func writeJSONAtomic(path string, value any) error {
	if err := rejectSymlinkIfPresent(path); err != nil {
		return err
	}
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode state: %v", ErrStateUnsafe, err)
	}
	content = append(content, '\n')
	if len(content) > maxStateFileBytes {
		return fmt.Errorf("%w: state file too large", ErrStateUnsafe)
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".state-")
	if err != nil {
		return fmt.Errorf("%w: create state temp: %v", ErrStateUnsafe, err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("%w: protect state temp: %v", ErrStateUnsafe, err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("%w: write state temp: %v", ErrStateUnsafe, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("%w: sync state temp: %v", ErrStateUnsafe, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close state temp: %v", ErrStateUnsafe, err)
	}
	if err := atomicReplace(temporaryPath, path); err != nil {
		return fmt.Errorf("%w: replace state: %v", ErrStateUnsafe, err)
	}
	cleanup = false
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("%w: sync state directory: %v", ErrStateUnsafe, err)
	}
	return nil
}

func readJSON(path string, target any) (bool, error) {
	if err := rejectSymlinkIfPresent(path); err != nil {
		return false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: read state: %v", ErrStateUnsafe, err)
	}
	if len(content) < 1 || len(content) > maxStateFileBytes {
		return false, fmt.Errorf("%w: state byte length", ErrStateUnsafe)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, fmt.Errorf("%w: decode state: %v", ErrStateUnsafe, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("%w: state trailing data", ErrStateUnsafe)
	}
	return true, nil
}

func ensureSecureDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: directory is a symlink or non-directory", ErrStateUnsafe)
		}
		if runtime.GOOS != "windows" {
			if err := os.Chmod(path, 0o700); err != nil {
				return fmt.Errorf("%w: directory permissions: %v", ErrStateUnsafe, err)
			}
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect directory: %v", ErrStateUnsafe, err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return fmt.Errorf("%w: create directory: %v", ErrStateUnsafe, err)
	}
	return nil
}

func secureAbsolute(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrStateUnsafe)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("%w: absolute path: %v", ErrStateUnsafe, err)
	}
	absolute = filepath.Clean(absolute)
	cursor := absolute
	missing := []string{}
	for {
		info, err := os.Lstat(cursor)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("%w: symlink path component", ErrStateUnsafe)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: resolve path: %v", ErrStateUnsafe, err)
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("%w: no existing path prefix", ErrStateUnsafe)
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
	resolved, err := filepath.EvalSymlinks(cursor)
	if err != nil {
		return "", fmt.Errorf("%w: resolve existing path: %v", ErrStateUnsafe, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(left, right string) bool {
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(parent, child string) bool {
	if runtime.GOOS == "windows" {
		parent = strings.ToLower(parent)
		child = strings.ToLower(child)
	}
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func ensureContained(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("%w: path escapes root", ErrStateUnsafe)
	}
	return nil
}

func rejectSymlinkIfPresent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect path: %v", ErrStateUnsafe, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: symbolic link", ErrStateUnsafe)
	}
	return nil
}

func randomID(prefix string, byteCount int) (string, error) {
	raw := make([]byte, byteCount)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("%w: random id: %v", ErrStateUnsafe, err)
	}
	return prefix + hex.EncodeToString(raw), nil
}

func safeIdentifier(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+32 {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}

func canonicalNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
