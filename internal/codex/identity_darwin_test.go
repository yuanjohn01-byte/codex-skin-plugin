//go:build darwin

package codex

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCurrentOfficialCodexIdentity(t *testing.T) {
	if os.Getenv("CODEX_SKIN_REAL_CODEX") != "1" {
		t.Skip("set CODEX_SKIN_REAL_CODEX=1 for the local Gate B identity probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := verifyDarwinBundle(ctx, "/Applications/ChatGPT.app"); err != nil {
		t.Fatalf("verifyDarwinBundle() error = %v", err)
	}
	installation, err := DiscoverInstallation(ctx)
	if err != nil {
		t.Fatalf("DiscoverInstallation() error = %v", err)
	}
	if installation.Platform != "macos" ||
		installation.AppIdentifier != officialBundleID ||
		installation.Publisher != officialTeamID ||
		installation.Version == "" ||
		len(installation.ExecutableSHA256) != 64 {
		t.Fatalf("installation = %#v", installation)
	}
	t.Logf(
		"verified appIdentifier=%s publisher=%s version=%s executableSHA256=%s",
		installation.AppIdentifier,
		installation.Publisher,
		installation.Version,
		installation.ExecutableSHA256,
	)
}
