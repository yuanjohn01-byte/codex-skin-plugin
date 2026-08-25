package restartflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

func TestApplyContinuationIsSingleUseAndReverified(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, filepath.Join(t.TempDir(), "plugin-cache")); err != nil {
		t.Fatal(err)
	}
	source := copyFixture(t)
	verified := verifyFixture(t, source)
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.StageApply(verified)
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != StatusPending || request.ThemePublicID != "100001" {
		t.Fatalf("staged request = %#v", request)
	}
	if _, err := store.StageRestore(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second active request error = %v", err)
	}
	if err := os.WriteFile(source, []byte("source changed after staging"), 0o600); err != nil {
		t.Fatal(err)
	}
	request, err = store.Approve(request.RequestID)
	if err != nil || request.Status != StatusApproved {
		t.Fatalf("approve = %#v, %v", request, err)
	}
	request, err = store.Begin(request.RequestID)
	if err != nil || request.Status != StatusRunning || !request.RestartStarted {
		t.Fatalf("begin = %#v, %v", request, err)
	}
	reverified, err := store.LoadVerified(request)
	if err != nil {
		t.Fatalf("LoadVerified() error = %v", err)
	}
	if reverified.PackageSHA256 != verified.PackageSHA256 {
		t.Fatalf("reverified digest = %s", reverified.PackageSHA256)
	}
	request, err = store.Complete(
		request.RequestID,
		"op_0123456789abcdef0123456789abcdef",
		"100001",
		"1.0.0",
	)
	if err != nil || request.Status != StatusCompleted {
		t.Fatalf("complete = %#v, %v", request, err)
	}
	if _, err := store.Begin(request.RequestID); !errors.Is(err, ErrState) {
		t.Fatalf("completed request was reused: %v", err)
	}
}

func TestPrepareNewApplySupersedesOnlyUnapprovedRequest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.StageApply(verifyFixture(t, copyFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	payload := store.payloadDirectory(first.RequestID)
	if superseded, err := store.PrepareNewApply(); err != nil || !superseded {
		t.Fatalf("PrepareNewApply() = %t, %v", superseded, err)
	}
	if _, found, err := store.Current(); err != nil || found {
		t.Fatalf("current after supersede = found:%t err:%v", found, err)
	}
	if _, err := os.Lstat(payload); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded payload remains: %v", err)
	}
	second, err := store.StageApply(verifyFixture(t, copyFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(second.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareNewApply(); !errors.Is(err, ErrBusy) {
		t.Fatalf("approved request was replaced: %v", err)
	}
	current, found, err := store.Current()
	if err != nil || !found || current.RequestID != second.RequestID || current.Status != StatusApproved {
		t.Fatalf("approved current = %#v, found=%t err=%v", current, found, err)
	}
}

func TestPrepareNewApplyRetiresTerminalRestartRecord(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.StageApply(verifyFixture(t, copyFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Approve(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Begin(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Complete(
		request.RequestID,
		"op_0123456789abcdef0123456789abcdef",
		request.ThemePublicID,
		request.ThemeVersion,
	); err != nil {
		t.Fatal(err)
	}
	if superseded, err := store.PrepareNewApply(); err != nil || superseded {
		t.Fatalf("PrepareNewApply() after completed = %t, %v", superseded, err)
	}
	if _, found, err := store.Current(); err != nil || found {
		t.Fatalf("terminal current was retained: found:%t err:%v", found, err)
	}
}

func TestTerminalRequestIsArchivedBeforeRestoreReplacesCurrent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	apply, err := store.StageApply(verifyFixture(t, copyFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Approve(apply.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(apply.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fail(apply.RequestID, "CS-FLOW-ROLLBACK-001"); err != nil {
		t.Fatal(err)
	}
	restore, err := store.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	current, found, err := store.Current()
	if err != nil || !found || current.RequestID != restore.RequestID || current.Kind != operationRestore {
		t.Fatalf("current restore = %#v, found=%t, err=%v", current, found, err)
	}
	history, err := store.History()
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %#v, err=%v", history, err)
	}
	if history[0].RequestID != apply.RequestID || history[0].Status != StatusFailed ||
		history[0].ErrorCode != "CS-FLOW-ROLLBACK-001" {
		t.Fatalf("archived apply = %#v", history[0])
	}
}

func TestContinuationTamperAndSymlinkFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.StageApply(verifyFixture(t, copyFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Approve(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Begin(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(
		root,
		"restart",
		requestsDirname,
		request.RequestID,
		packageFilename,
	)
	if err := os.WriteFile(payload, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadVerified(request); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("tampered payload error = %v", err)
	}

	otherRoot := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(otherRoot, ""); err != nil {
		t.Fatal(err)
	}
	other, err := New(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	request, err = other.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(otherRoot, "restart", currentFilename)
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "restart", currentFilename), current); err != nil {
		t.Fatal(err)
	}
	if _, _, err := other.Current(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlink current error = %v", err)
	}
}

func TestRestoreContinuationExpiresAndCompletesWithoutPayload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	request, err := store.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(requestTTL + time.Second)
	if _, err := store.Approve(request.RequestID); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired approval error = %v", err)
	}

	now = now.Add(time.Minute)
	request, err = store.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Approve(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Begin(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Complete(
		request.RequestID,
		"op_abcdef0123456789abcdef0123456789",
		"",
		"",
	)
	if err != nil || request.Kind != operationRestore ||
		request.Status != StatusCompleted {
		t.Fatalf("restore complete = %#v, %v", request, err)
	}
}

func TestBeginRenewsContinuationForEntireWorkerLease(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	request, err := store.StageRestore()
	if err != nil {
		t.Fatal(err)
	}

	// Begin at the very end of the user confirmation window.
	now = now.Add(requestTTL - time.Second)
	request, err = store.Approve(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	request, err = store.Begin(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt, err := time.Parse(time.RFC3339, request.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(RestartRunningLease); !expiresAt.Equal(want) {
		t.Fatalf("running expiry = %s, want %s", expiresAt, want)
	}

	// A restore cannot replace the continuation while the worker can still be
	// applying, verifying, rolling back, or committing its terminal status.
	now = now.Add(RestartWorkerTimeout + 2*time.Second)
	if _, err := store.StageRestore(); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement during worker lease error = %v", err)
	}

	now = expiresAt.Add(time.Second)
	replacement, err := store.StageRestore()
	if err != nil || replacement.RequestID == request.RequestID {
		t.Fatalf("replacement after worker lease = %#v, %v", replacement, err)
	}
}

func TestContinuationLockIsProcessScoped(t *testing.T) {
	root := filepath.Join(t.TempDir(), "CodexSkin")
	if _, err := engine.OpenStore(root, ""); err != nil {
		t.Fatal(err)
	}
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.lock(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second lock error = %v", err)
	}
	unlock()
	reacquired, err := store.lock()
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	reacquired()
}

func copyFixture(t *testing.T) string {
	t.Helper()
	source := filepath.Join(
		"..",
		"..",
		"fixtures",
		"free-test-theme-v1",
		"signed-release-v1",
		"package.cskin",
	)
	content, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "package.cskin")
	if err := os.WriteFile(destination, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return destination
}

func verifyFixture(t *testing.T, packagePath string) theme.Verified {
	t.Helper()
	fixture := filepath.Join(
		"..",
		"..",
		"fixtures",
		"free-test-theme-v1",
		"signed-release-v1",
	)
	descriptor, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.json"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join(fixture, "release-descriptor.sig"))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(packagePath, descriptor, signature)
	if err != nil {
		t.Fatal(err)
	}
	return verified
}
