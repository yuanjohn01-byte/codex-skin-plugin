package sessionflow

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

func TestSessionLifecycleIsSingleOwnerAndStopsCooperatively(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fixedNow := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }
	identity := testIdentity()

	record, err := store.Start("100021", "1.0.1", testDigest(), identity)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if record.Status != StatusStarting || record.ControllerPID != 0 {
		t.Fatalf("initial record = %#v", record)
	}
	if _, err := store.Start("100022", "1.0.1", testDigest(), identity); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start() error = %v, want ErrBusy", err)
	}

	if _, err := store.Claim(record.SessionID, 4312); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Activate(record.SessionID, 4312); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	active, found, err := store.Current()
	if err != nil || !found || active.Status != StatusActive || active.ControllerPID != 4312 {
		t.Fatalf("active record = %#v, found = %t, err = %v", active, found, err)
	}
	if _, err := store.Heartbeat(record.SessionID, 4313); !errors.Is(err, ErrState) {
		t.Fatalf("wrong-owner heartbeat error = %v, want ErrState", err)
	}

	stopping, requested, err := store.RequestStop()
	if err != nil || !requested || stopping.Status != StatusStopping {
		t.Fatalf("RequestStop() = %#v, %t, %v", stopping, requested, err)
	}
	if requestedAgain, err := store.StopRequested(record.SessionID); err != nil || !requestedAgain {
		t.Fatalf("StopRequested() = %t, %v", requestedAgain, err)
	}
	if _, err := store.Heartbeat(record.SessionID, 4312); !errors.Is(err, ErrState) {
		t.Fatalf("heartbeat after stop request error = %v, want ErrState", err)
	}
	if _, err := store.Finish(record.SessionID, StatusEnded, "restore_requested"); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	ended, found, err := store.Current()
	if err != nil || !found || ended.Status != StatusEnded || ended.EndedReason != "restore_requested" {
		t.Fatalf("ended record = %#v, found = %t, err = %v", ended, found, err)
	}
	if _, requested, err := store.RequestStop(); err != nil || requested {
		t.Fatalf("RequestStop() after end = requested %t, err %v", requested, err)
	}
}

func TestSessionStoreRejectsTamperedRecord(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	record, err := store.Start("100021", "1.0.1", testDigest(), testIdentity())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	path := filepath.Join(root, "session", "current.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":1,"sessionId":"`+record.SessionID+`","unknown":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, _, err := store.Current(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Current() error = %v, want ErrUnsafe", err)
	}
}

func TestSessionCanEndBeforeControllerClaimsPID(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	record, err := store.Start("100021", "1.0.1", testDigest(), testIdentity())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ended, requested, err := store.RequestStop()
	if err != nil || !requested || ended.Status != StatusEnded || ended.EndedReason != "stop_before_claim" {
		t.Fatalf("RequestStop() = %#v, %t, %v", ended, requested, err)
	}
	if _, err := store.Claim(record.SessionID, 4312); !errors.Is(err, ErrState) {
		t.Fatalf("late Claim() error = %v, want ErrState", err)
	}
}

func TestSessionCanFailBeforeControllerClaimsPID(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	record, err := store.Start("100021", "1.0.1", testDigest(), testIdentity())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	failed, err := store.Finish(record.SessionID, StatusFailed, "controller_launch_failed")
	if err != nil || failed.Status != StatusFailed || failed.ControllerPID != 0 {
		t.Fatalf("Finish() = %#v, %v", failed, err)
	}
}

func TestFreshRequiresRecentInProgressHeartbeat(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	fixedNow := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixedNow }
	record, err := store.Start("100021", "1.0.1", testDigest(), testIdentity())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !record.Fresh(fixedNow.Add(10*time.Second), 30*time.Second) {
		t.Fatal("recent starting record should be fresh")
	}
	if record.Fresh(fixedNow.Add(31*time.Second), 30*time.Second) {
		t.Fatal("stale starting record must not be fresh")
	}
	store.now = func() time.Time { return fixedNow.Add(31 * time.Second) }
	failed, expired, err := store.ExpireStale(30*time.Second, "controller_heartbeat_lost")
	if err != nil || !expired || failed.Status != StatusFailed || failed.EndedReason != "controller_heartbeat_lost" {
		t.Fatalf("ExpireStale() = %#v, %t, %v", failed, expired, err)
	}
	if failed.Fresh(fixedNow, 30*time.Second) {
		t.Fatal("terminal record must not be a fresh controller claim")
	}
	if _, expiredAgain, err := store.ExpireStale(30*time.Second, "controller_heartbeat_lost"); err != nil || expiredAgain {
		t.Fatalf("second ExpireStale() expired = %t, err = %v", expiredAgain, err)
	}
}

func TestCurrentToleratesAtomicHeartbeatPublication(t *testing.T) {
	root := t.TempDir()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Start("100021", "1.0.1", testDigest(), testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(record.SessionID, 4312); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Activate(record.SessionID, 4312); err != nil {
		t.Fatal(err)
	}

	var readers sync.WaitGroup
	errorsSeen := make(chan error, 8)
	for reader := 0; reader < 8; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for read := 0; read < 250; read++ {
				current, found, err := store.Current()
				if err != nil || !found || current.SessionID != record.SessionID {
					select {
					case errorsSeen <- err:
					default:
					}
					return
				}
			}
		}()
	}
	for heartbeat := 0; heartbeat < 250; heartbeat++ {
		if _, err := store.Heartbeat(record.SessionID, 4312); err != nil {
			t.Fatal(err)
		}
	}
	readers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatalf("Current() observed an incomplete atomic publication: %v", err)
	}
}

func testDigest() string {
	return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}

func testIdentity() engine.Identity {
	return engine.Identity{
		Platform:       "macos",
		AppIdentifier:  "com.openai.codex",
		Publisher:      "OpenAI",
		Version:        "26.727.0",
		ExecutableHash: testDigest(),
		ProcessID:      321,
		ProcessStartID: "171234567890",
	}
}
