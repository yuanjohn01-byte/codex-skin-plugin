package engine

import (
	"context"
	"errors"
)

const (
	StateSchemaVersion     = 1
	CurrentEngineVersion   = "0.2.0"
	MinimumTemplateVersion = 1
	TemplateVersion        = 7
	MarkerID               = "codex-skin-theme-v1"
	RootMarkerAttribute    = "data-codex-skin"
	ThemeMarkerAttribute   = "data-codex-skin-theme"
	ThemeVersionAttribute  = "data-codex-skin-theme-version"
	VersionMarkerAttribute = "data-codex-skin-template"
)

var (
	ErrConfiguration     = errors.New("theme engine configuration is invalid")
	ErrBusy              = errors.New("another theme operation is active")
	ErrStateUnsafe       = errors.New("theme engine state path is unsafe")
	ErrCapabilityBlocked = errors.New("Codex capability probes blocked theme apply")
	ErrRestartConsent    = errors.New("current Codex requires an explicit restart confirmation")
	ErrApplyFailed       = errors.New("theme apply failed")
	ErrVerifyFailed      = errors.New("theme verification failed")
	ErrRollbackFailed    = errors.New("theme rollback failed")
	ErrRestoreFailed     = errors.New("official appearance restore failed")
)

type Identity struct {
	Platform       string `json:"platform"`
	AppIdentifier  string `json:"appIdentifier"`
	Publisher      string `json:"publisher"`
	Version        string `json:"version"`
	ExecutableHash string `json:"executableHash"`
	ProcessID      int    `json:"processId"`
	ProcessStartID string `json:"processStartId"`
}

type Session struct {
	Identity Identity
	OpaqueID string
}

type RegionStatus string

const (
	RegionPass       RegionStatus = "pass"
	RegionNotPresent RegionStatus = "not_present"
	RegionFail       RegionStatus = "fail"
)

type RegionReport struct {
	Scope              string                  `json:"scope,omitempty"`
	RuntimeVersion     int                     `json:"runtimeVersion,omitempty"`
	StyleMarkerCount   int                     `json:"styleMarkerCount"`
	TemplateVersion    int                     `json:"templateVersion"`
	ThemePublicID      string                  `json:"themePublicId"`
	BackgroundLoaded   bool                    `json:"backgroundLoaded"`
	BackgroundTokenSet bool                    `json:"backgroundTokenSet,omitempty"`
	BodyBackgroundSet  bool                    `json:"bodyBackgroundSet,omitempty"`
	Regions            map[string]RegionStatus `json:"regions"`
}

// ThemeVerificationResult is the bounded, post-injection verification result.
// It deliberately contains only renderer contract fields, never DOM text,
// conversation data, screenshots, or raw CDP errors.
type ThemeVerificationResult struct {
	Report           RegionReport
	Attempts         int
	ReapplyAttempted bool
	ProbeCompleted   bool
}

type Snapshot struct {
	StylePresent      bool   `json:"stylePresent"`
	StyleText         string `json:"styleText"`
	BackgroundDataURL string `json:"backgroundDataURL"`
	ThemePublicID     string `json:"themePublicId"`
	ThemeVersion      string `json:"themeVersion"`
	TemplateVersion   int    `json:"templateVersion"`
	AppearanceMode    string `json:"appearanceMode"`
}

type CompiledTheme struct {
	ThemePublicID     string
	ThemeVersion      string
	TemplateVersion   int
	AppearanceMode    string
	StyleText         string
	PreviousStyleText string
	LegacyStyleText   string
	BackgroundDataURL string
}

type Adapter interface {
	OpenVerifiedSession(context.Context) (Session, error)
	Probe(context.Context, Session) (RegionReport, error)
	Capture(context.Context, Session) (Snapshot, error)
	Apply(context.Context, Session, CompiledTheme) error
	Verify(context.Context, Session, CompiledTheme) (RegionReport, error)
	Restore(context.Context, Session, Snapshot) error
	RestoreOfficial(context.Context, Session) error
	VerifyOfficial(context.Context, Session) error
	Close(context.Context, Session) error
}

// CapabilityWaiter lets a live adapter wait for the official UI to finish
// loading before the engine evaluates fail-closed capability probes.
type CapabilityWaiter interface {
	WaitForCapabilities(context.Context, Session) (RegionReport, error)
}

// ThemeVerificationWaiter lets a live adapter wait for the renderer controller
// and Codex shell to settle after injection. The engine still commits only when
// the same strict report predicate passes; waiting never converts an unknown
// or failed renderer into success.
type ThemeVerificationWaiter interface {
	WaitForThemeVerification(context.Context, Session, CompiledTheme) (ThemeVerificationResult, error)
}

// SessionPrimer lets an adapter re-establish trusted in-memory context from a
// package that the engine revalidated from its offline cache.
type SessionPrimer interface {
	Prime(context.Context, Session, CompiledTheme) error
}

// ThemeSessionOpener lets the live adapter synchronize Codex's native
// appearance before opening the verified renderer used for a theme apply.
// Test/fake adapters keep using Adapter.OpenVerifiedSession.
type ThemeSessionOpener interface {
	OpenVerifiedThemeSession(context.Context, CompiledTheme) (Session, error)
}

// OfficialSessionOpener lets the live adapter restore the exact native
// appearance backup before the official renderer is verified.
type OfficialSessionOpener interface {
	OpenVerifiedOfficialSession(context.Context) (Session, error)
}

// OfficialRollbackFinalizer completes a failed first-theme transaction after
// the verified renderer has been restored to the official interface. The live
// adapter uses it to stop only the exact controlled process, restore Codex's
// native appearance preference, and reopen an ordinary Codex instance.
type OfficialRollbackFinalizer interface {
	FinalizeOfficialRollback(context.Context, Session) error
}

type ApplyResult struct {
	OperationID   string
	ThemePublicID string
	ThemeVersion  string
	Identity      Identity
	Report        RegionReport
	RecoveryPoint string
}

type RestoreResult struct {
	OperationID string
	Identity    Identity
	WasThemed   bool
}
