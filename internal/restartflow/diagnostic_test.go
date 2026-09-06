package restartflow

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/restarttrace"
)

func TestDiagnosticIsBoundedNonAuthoritativeAndRequestBound(t *testing.T) {
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request, err := store.StageRestore()
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.currentPath)
	if err != nil {
		t.Fatal(err)
	}
	report := restarttrace.Report{SchemaVersion: 1, RequestID: request.RequestID, HelperVersion: "test", Events: []restarttrace.Entry{}}
	if err := store.WriteDiagnostic(report); err != nil {
		t.Fatal(err)
	}
	report.FirstFailureStage = "stop_process"
	if err := store.WriteDiagnostic(report); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(store.currentPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("diagnostic authorized or changed request")
	}
	path := filepath.Join(store.directory, "diagnostic.json")
	before, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*restarttrace.Report){
		func(r *restarttrace.Report) { r.RequestID = "../outside" },
		func(r *restarttrace.Report) { r.RequestID = "rst_" + strings.Repeat("a", 32) },
		func(r *restarttrace.Report) { r.HelperVersion = strings.Repeat("a", 17*1024) },
		func(r *restarttrace.Report) { r.Events = make([]restarttrace.Entry, restarttrace.MaxEntries+1) },
	} {
		bad := report
		mutate(&bad)
		if err := store.WriteDiagnostic(bad); err == nil {
			t.Fatal("unsafe report accepted")
		}
	}
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected report overwrote diagnostic")
	}
	if _, err := store.Begin(request.RequestID); !errors.Is(err, ErrState) {
		t.Fatal("diagnostic bypassed consent")
	}
}

func TestDiagnosticRejectsNonRegularTarget(t *testing.T) {
	for _, target := range []string{"directory", "symlink"} {
		t.Run(target, func(t *testing.T) {
			store, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			request, err := store.StageRestore()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.directory, "diagnostic.json")
			outside := filepath.Join(t.TempDir(), "untouched")
			if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
				t.Fatal(err)
			}
			if target == "directory" {
				err = os.Mkdir(path, 0o700)
			} else {
				err = os.Symlink(outside, path)
			}
			if err != nil && target == "symlink" {
				t.Skip("symlink creation unavailable")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteDiagnostic(restarttrace.Report{SchemaVersion: 1, RequestID: request.RequestID}); !errors.Is(err, ErrUnsafe) {
				t.Fatalf("unsafe target: %v", err)
			}
			raw, err := os.ReadFile(outside)
			if err != nil || string(raw) != "untouched" {
				t.Fatal("outside target changed")
			}
		})
	}
}
