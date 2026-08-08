package themeapi_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/credentials"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/deviceauth"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/themeapi"
)

const stagingThemeOrigin = "https://codex-skin-staging.yuanjohn01.workers.dev"
const stagingFreeThemePublicID = "100002"

func TestStagingThemeReleaseLocalE2E(t *testing.T) {
	if os.Getenv("CODEX_SKIN_STAGING_THEME_E2E") != "staging-only" {
		t.Skip("set CODEX_SKIN_STAGING_THEME_E2E=staging-only for the local Staging theme probe")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	root, err := engine.DefaultRoot("darwin", home, "")
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := flowstate.New(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := stateStore.Read()
	if err != nil || state.DeviceID == "" {
		t.Fatalf("read linked device state: %v", err)
	}
	credentialStore, err := credentials.New()
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	authClient, err := deviceauth.NewClient(
		stagingThemeOrigin,
		httpClient,
		credentialStore,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	refreshed, err := authClient.Refresh(ctx, state.DeviceID)
	if err != nil {
		t.Fatalf("refresh linked device: %v", err)
	}
	if refreshed.Outcome != deviceauth.OutcomeAuthorized ||
		refreshed.AccessToken == nil {
		t.Fatalf(
			"refresh outcome=%s errorCode=%s",
			refreshed.Outcome,
			refreshed.ErrorCode,
		)
	}
	themeClient, err := themeapi.NewClient(stagingThemeOrigin, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := themeClient.Metadata(
		ctx,
		stagingFreeThemePublicID,
		refreshed.AccessToken.Value(),
	)
	if err != nil {
		t.Fatalf("theme metadata: %v", err)
	}
	if metadata.Outcome != themeapi.OutcomeReady || metadata.Release == nil {
		t.Fatalf(
			"theme metadata outcome=%s errorCode=%s",
			metadata.Outcome,
			metadata.ErrorCode,
		)
	}
	destination := filepath.Join(t.TempDir(), "theme.part")
	if err := themeClient.Download(
		ctx,
		*metadata.Release,
		refreshed.AccessToken.Value(),
		destination,
	); err != nil {
		t.Fatalf("theme download: %v", err)
	}
	verified, err := theme.Verify(
		destination,
		metadata.Release.DescriptorBytes,
		metadata.Release.SignatureBytes,
	)
	if err != nil {
		t.Fatalf("theme verification: %v", err)
	}
	t.Logf(
		"verified Staging theme=%s version=%s tier=%s packageSHA256=%s",
		verified.Manifest.ThemePublicID,
		verified.Manifest.ThemeVersion,
		metadata.Release.Tier,
		verified.PackageSHA256,
	)
}
