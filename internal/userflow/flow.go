// Package userflow coordinates one user instruction across authorization,
// optional purchase, trusted download, verification, and Gate B apply.
package userflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/deviceauth"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/flowstate"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/themeapi"
)

const (
	purchasePollInterval = 4 * time.Second
	purchaseWaitLimit    = 5 * time.Minute
)

var (
	ErrConfiguration = errors.New("Plugin user flow configuration is invalid")
	ErrAuthorization = errors.New("Plugin authorization did not complete")
	ErrAccess        = errors.New("theme access did not become ready")
	ErrTheme         = errors.New("theme could not be downloaded or verified")
	ErrApply         = errors.New("theme could not be applied")
)

type AuthClient interface {
	Start(context.Context, deviceauth.StartInput) (deviceauth.StartResult, error)
	Refresh(context.Context, string) (deviceauth.Result, error)
	AuthorizeAndContinue(context.Context, deviceauth.Continuation) (deviceauth.Result, error)
}

type ThemeClient interface {
	Metadata(context.Context, string, string) (themeapi.Result, error)
	Download(context.Context, themeapi.Release, string, string) error
}

type StateStore interface {
	Read() (flowstate.State, error)
	Write(flowstate.State) error
}

type Applier interface {
	Apply(context.Context, themeapi.Release, string) (engine.ApplyResult, error)
}

type Config struct {
	Root              string
	BaseURL           string
	Auth              AuthClient
	Themes            ThemeClient
	State             StateStore
	Applier           Applier
	OpenURL           func(context.Context, string) error
	Wait              func(context.Context, time.Duration) error
	Now               func() time.Time
	DeviceDisplayName string
	Platform          string
	PluginVersion     string
	EngineVersion     string
}

type ApplyResult struct {
	OperationID   string
	ThemePublicID string
	ThemeVersion  string
	Authorized    bool
	PurchaseShown bool
}

type Runner struct {
	config  Config
	baseURL *url.URL
}

func New(config Config) (*Runner, error) {
	baseURL, err := url.Parse(config.BaseURL)
	if err != nil ||
		!baseURL.IsAbs() ||
		baseURL.User != nil ||
		baseURL.Path != "" ||
		baseURL.RawQuery != "" ||
		baseURL.Fragment != "" ||
		config.Root == "" ||
		!filepath.IsAbs(config.Root) ||
		config.Auth == nil ||
		config.Themes == nil ||
		config.State == nil ||
		config.Applier == nil ||
		config.OpenURL == nil ||
		config.DeviceDisplayName == "" ||
		config.Platform == "" ||
		config.PluginVersion == "" ||
		config.EngineVersion == "" {
		return nil, ErrConfiguration
	}
	if config.Wait == nil {
		config.Wait = waitForContext
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Runner{config: config, baseURL: baseURL}, nil
}

func (runner *Runner) Apply(ctx context.Context, themePublicID string) (ApplyResult, error) {
	if len(themePublicID) != 6 {
		return ApplyResult{}, ErrTheme
	}
	for _, character := range themePublicID {
		if character < '0' || character > '9' {
			return ApplyResult{}, ErrTheme
		}
	}
	state, err := runner.config.State.Read()
	if err != nil {
		return ApplyResult{}, ErrAuthorization
	}
	state.PendingThemePublicID = themePublicID
	if err := runner.config.State.Write(state); err != nil {
		return ApplyResult{}, ErrAuthorization
	}

	accessToken, device, newlyAuthorized, err := runner.authorize(ctx, state)
	if err != nil {
		return ApplyResult{}, err
	}
	if device.ID != state.DeviceID {
		state.DeviceID = device.ID
		if err := runner.config.State.Write(state); err != nil {
			return ApplyResult{}, ErrAuthorization
		}
	}

	release, purchaseShown, err := runner.awaitRelease(ctx, themePublicID, accessToken)
	if err != nil {
		return ApplyResult{}, err
	}
	temporaryPath, err := randomPartPath(filepath.Join(runner.config.Root, "tmp"))
	if err != nil {
		return ApplyResult{}, ErrTheme
	}
	defer os.Remove(temporaryPath)
	if err := runner.config.Themes.Download(ctx, release, accessToken, temporaryPath); err != nil {
		return ApplyResult{}, errors.Join(ErrTheme, err)
	}
	applied, err := runner.config.Applier.Apply(ctx, release, temporaryPath)
	if err != nil {
		return ApplyResult{}, errors.Join(ErrApply, err)
	}
	state.PendingThemePublicID = ""
	if err := runner.config.State.Write(state); err != nil {
		return ApplyResult{}, ErrAuthorization
	}
	return ApplyResult{
		OperationID:   applied.OperationID,
		ThemePublicID: applied.ThemePublicID,
		ThemeVersion:  applied.ThemeVersion,
		Authorized:    newlyAuthorized,
		PurchaseShown: purchaseShown,
	}, nil
}

func (runner *Runner) authorize(ctx context.Context, state flowstate.State) (string, deviceauth.Device, bool, error) {
	if state.DeviceID != "" {
		refreshed, err := runner.config.Auth.Refresh(ctx, state.DeviceID)
		if err != nil {
			return "", deviceauth.Device{}, false, errors.Join(ErrAuthorization, err)
		}
		if refreshed.Outcome == deviceauth.OutcomeAuthorized && refreshed.AccessToken != nil {
			return refreshed.AccessToken.Value(), refreshed.Device, false, nil
		}
		if refreshed.Outcome != deviceauth.OutcomeReauthorize {
			return "", deviceauth.Device{}, false, ErrAuthorization
		}
	}
	started, err := runner.config.Auth.Start(ctx, deviceauth.StartInput{
		DeviceDisplayName: runner.config.DeviceDisplayName,
		Platform:          runner.config.Platform,
		PluginVersion:     runner.config.PluginVersion,
		EngineVersion:     runner.config.EngineVersion,
	})
	if err != nil || started.Outcome != deviceauth.OutcomeStarted {
		return "", deviceauth.Device{}, false, errors.Join(ErrAuthorization, err)
	}
	if err := runner.config.OpenURL(ctx, started.VerificationURL); err != nil {
		return "", deviceauth.Device{}, false, errors.Join(ErrAuthorization, err)
	}
	authorized, err := runner.config.Auth.AuthorizeAndContinue(ctx, deviceauth.Continuation{
		Credentials:     started.Credentials,
		InitialInterval: started.Interval,
		AwaitDeviceSlot: func(ctx context.Context, limit deviceauth.DeviceLimit) error {
			if limit.ManagementURL == "" {
				return ErrAuthorization
			}
			return runner.config.OpenURL(ctx, limit.ManagementURL)
		},
		Run: func(context.Context, deviceauth.Result) error { return nil },
	})
	if err != nil ||
		authorized.Outcome != deviceauth.OutcomeAuthorized ||
		authorized.AccessToken == nil ||
		authorized.Device.ID == "" {
		return "", deviceauth.Device{}, false, errors.Join(ErrAuthorization, err)
	}
	return authorized.AccessToken.Value(), authorized.Device, true, nil
}

func (runner *Runner) awaitRelease(ctx context.Context, themePublicID, accessToken string) (themeapi.Release, bool, error) {
	deadline := runner.config.Now().Add(purchaseWaitLimit)
	purchaseShown := false
	for {
		result, err := runner.config.Themes.Metadata(ctx, themePublicID, accessToken)
		if err != nil {
			return themeapi.Release{}, purchaseShown, errors.Join(ErrTheme, err)
		}
		switch result.Outcome {
		case themeapi.OutcomeReady:
			if result.Release == nil {
				return themeapi.Release{}, purchaseShown, ErrTheme
			}
			return *result.Release, purchaseShown, nil
		case themeapi.OutcomeAccessRequired:
			if purchaseShown {
				if !runner.config.Now().Before(deadline) {
					return themeapi.Release{}, true, ErrAccess
				}
			} else {
				purchaseURL := runner.baseURL.ResolveReference(&url.URL{
					Path:     "/pricing",
					RawQuery: "theme=" + themePublicID,
				})
				if result.PricingPath != purchaseURL.RequestURI() {
					return themeapi.Release{}, false, ErrTheme
				}
				if err := runner.config.OpenURL(ctx, purchaseURL.String()); err != nil {
					return themeapi.Release{}, false, errors.Join(ErrAccess, err)
				}
				purchaseShown = true
			}
			if err := runner.config.Wait(ctx, purchasePollInterval); err != nil {
				return themeapi.Release{}, purchaseShown, errors.Join(ErrAccess, err)
			}
		case themeapi.OutcomeReauthorize:
			return themeapi.Release{}, purchaseShown, ErrAuthorization
		case themeapi.OutcomeRetry:
			if !runner.config.Now().Before(deadline) {
				return themeapi.Release{}, purchaseShown, ErrTheme
			}
			if err := runner.config.Wait(ctx, purchasePollInterval); err != nil {
				return themeapi.Release{}, purchaseShown, errors.Join(ErrTheme, err)
			}
		default:
			return themeapi.Release{}, purchaseShown, ErrTheme
		}
	}
}

func randomPartPath(directory string) (string, error) {
	if !filepath.IsAbs(directory) {
		return "", ErrTheme
	}
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return filepath.Join(directory, "theme-"+hex.EncodeToString(value)+".part"), nil
}

func waitForContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type EngineApplier struct {
	Engine *engine.Engine
}

func (applier EngineApplier) Apply(ctx context.Context, release themeapi.Release, packagePath string) (engine.ApplyResult, error) {
	if applier.Engine == nil {
		return engine.ApplyResult{}, ErrConfiguration
	}
	verified, err := theme.Verify(packagePath, release.DescriptorBytes, release.SignatureBytes)
	if err != nil {
		return engine.ApplyResult{}, err
	}
	if verified.Manifest.ThemePublicID != release.ThemePublicID ||
		verified.Manifest.ThemeVersion != release.ThemeVersion ||
		verified.Manifest.Compatibility.MinEngineVersion != release.MinEngineVersion ||
		!theme.EngineCompatible(engine.CurrentEngineVersion, release.MinEngineVersion) ||
		strings.TrimSpace(release.MinEngineVersion) != release.MinEngineVersion {
		return engine.ApplyResult{}, theme.ErrEngineIncompatible
	}
	return applier.Engine.ApplyVerified(ctx, verified)
}
