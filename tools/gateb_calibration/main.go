// gateb_calibration renders signed theme variants in an identity-verified,
// loopback-only Codex Desktop session. Outputs are local QA evidence.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/adapter"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

const maxIndexBytes = 256 * 1024

type calibrationIndex struct {
	SchemaVersion int       `json:"schemaVersion"`
	Status        string    `json:"status,omitempty"`
	PublishedAt   string    `json:"publishedAt"`
	SigningKeyID  string    `json:"signingKeyId"`
	Variants      []variant `json:"variants"`
}

type variant struct {
	ThemePublicID     string     `json:"themePublicId"`
	Name              string     `json:"name"`
	OptionID          string     `json:"optionId"`
	Recommended       bool       `json:"recommended"`
	ThemeVersion      string     `json:"themeVersion"`
	Parameters        parameters `json:"parameters"`
	PackageDirectory  string     `json:"packageDirectory"`
	PackageSHA256     string     `json:"packageSha256"`
	PreviewSHA256     string     `json:"previewSha256,omitempty"`
	PublishPlanSHA256 string     `json:"publishPlanSha256,omitempty"`
}

type parameters struct {
	BackgroundOverlay float64 `json:"backgroundOverlay"`
	SurfaceOpacity    float64 `json:"surfaceOpacity"`
	SurfaceBlurPx     int     `json:"surfaceBlurPx"`
}

type variantResult struct {
	ThemePublicID string              `json:"themePublicId"`
	Name          string              `json:"name"`
	OptionID      string              `json:"optionId"`
	Recommended   bool                `json:"recommended"`
	ThemeVersion  string              `json:"themeVersion"`
	Parameters    parameters          `json:"parameters"`
	PackageSHA256 string              `json:"packageSha256"`
	Screenshot    string              `json:"screenshot"`
	Report        engine.RegionReport `json:"report"`
}

type calibrationReport struct {
	SchemaVersion    int             `json:"schemaVersion"`
	CapturedAt       string          `json:"capturedAt"`
	Identity         engine.Identity `json:"identity"`
	Variants         []variantResult `json:"variants"`
	Transactions     int             `json:"transactions"`
	OfflineRestore   bool            `json:"offlineRestore"`
	OfficialRestored bool            `json:"officialRestored"`
}

type pinnedAdapter struct {
	live    *adapter.Live
	session engine.Session
}

func (pinned *pinnedAdapter) OpenVerifiedSession(context.Context) (engine.Session, error) {
	return pinned.session, nil
}

func (pinned *pinnedAdapter) Probe(ctx context.Context, _ engine.Session) (engine.RegionReport, error) {
	return pinned.live.Probe(ctx, pinned.session)
}

func (pinned *pinnedAdapter) Capture(ctx context.Context, _ engine.Session) (engine.Snapshot, error) {
	return pinned.live.Capture(ctx, pinned.session)
}

func (pinned *pinnedAdapter) Apply(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) error {
	return pinned.live.Apply(ctx, pinned.session, compiled)
}

func (pinned *pinnedAdapter) Verify(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) (engine.RegionReport, error) {
	return pinned.live.Verify(ctx, pinned.session, compiled)
}

func (pinned *pinnedAdapter) Restore(ctx context.Context, _ engine.Session, snapshot engine.Snapshot) error {
	return pinned.live.Restore(ctx, pinned.session, snapshot)
}

func (pinned *pinnedAdapter) RestoreOfficial(ctx context.Context, _ engine.Session) error {
	return pinned.live.RestoreOfficial(ctx, pinned.session)
}

func (pinned *pinnedAdapter) VerifyOfficial(ctx context.Context, _ engine.Session) error {
	return pinned.live.VerifyOfficial(ctx, pinned.session)
}

func (pinned *pinnedAdapter) Close(context.Context, engine.Session) error {
	return nil
}

func (pinned *pinnedAdapter) Prime(ctx context.Context, _ engine.Session, compiled engine.CompiledTheme) error {
	return pinned.live.Prime(ctx, pinned.session, compiled)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Gate B calibration failed: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) (returnErr error) {
	values, err := parseArguments(args)
	if err != nil {
		return err
	}
	indexPath := filepath.Clean(values["--index"])
	outputRoot := filepath.Clean(values["--out"])
	stateRoot := filepath.Clean(values["--root"])
	if !filepath.IsAbs(indexPath) || !filepath.IsAbs(outputRoot) ||
		!filepath.IsAbs(stateRoot) {
		return errors.New("all paths must be absolute")
	}
	index := calibrationIndex{}
	if err := readStrictJSON(indexPath, maxIndexBytes, &index); err != nil {
		return err
	}
	if index.SchemaVersion != 1 || (len(index.Variants) != 3 && len(index.Variants) != 9) {
		return errors.New("calibration index shape is invalid")
	}
	if err := selectVariants(&index, values["--only-theme"]); err != nil {
		return err
	}
	if _, err := os.Lstat(outputRoot); !errors.Is(err, os.ErrNotExist) {
		return errors.New("output directory must not exist")
	}
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return err
	}
	store, err := engine.OpenStore(stateRoot, filepath.Join(filepath.Dir(stateRoot), "synthetic-plugin-cache"))
	if err != nil {
		return err
	}
	live, err := adapter.NewLive(adapter.Config{Root: store.Root(), LaunchWait: 45 * time.Second})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	session, err := live.OpenVerifiedSession(ctx)
	if err != nil {
		return err
	}
	stopped := false
	defer func() {
		if !stopped {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer stopCancel()
			returnErr = errors.Join(returnErr, live.RestoreOfficial(stopCtx, session))
			returnErr = errors.Join(returnErr, live.StopOwned(stopCtx, session))
		}
	}()
	if err := waitForCoreCapabilities(ctx, live, session); err != nil {
		return err
	}
	before, err := live.Capture(ctx, session)
	if err != nil || before.StylePresent {
		return errors.New("controlled calibration session is not official")
	}
	report := calibrationReport{
		SchemaVersion: 1,
		CapturedAt:    time.Now().UTC().Format(time.RFC3339Nano),
		Identity:      session.Identity,
		Variants:      make([]variantResult, 0, len(index.Variants)),
	}
	indexRoot := filepath.Dir(indexPath)
	verifiedFinalists := make([]theme.Verified, 0, len(index.Variants))
	for _, item := range index.Variants {
		if err := validateVariant(item); err != nil {
			return err
		}
		packageRoot := filepath.Join(indexRoot, item.PackageDirectory)
		if err := requireContained(indexRoot, packageRoot); err != nil {
			return err
		}
		packagePath := filepath.Join(packageRoot, "package.cskin")
		descriptor, err := readLimited(filepath.Join(packageRoot, "release-descriptor.json"), 64*1024)
		if err != nil {
			return err
		}
		signature, err := readLimited(filepath.Join(packageRoot, "release-descriptor.sig"), 1024)
		if err != nil {
			return err
		}
		verified, err := theme.Verify(packagePath, descriptor, signature)
		if err != nil ||
			verified.PackageSHA256 != item.PackageSHA256 ||
			verified.Manifest.ThemePublicID != item.ThemePublicID ||
			verified.Manifest.ThemeVersion != item.ThemeVersion {
			return fmt.Errorf("variant %s package verification failed: %v", item.ThemePublicID+"-"+item.OptionID, err)
		}
		if len(index.Variants) == 3 {
			verifiedFinalists = append(verifiedFinalists, verified)
		}
		stageParent := filepath.Join(store.Root(), "tmp")
		stage, err := os.MkdirTemp(stageParent, "calibration-")
		if err != nil {
			return err
		}
		if err := os.Remove(stage); err != nil {
			return err
		}
		if err := theme.Extract(verified, stage); err != nil {
			return err
		}
		compiled, err := engine.CompileTheme(verified, stage)
		_ = os.RemoveAll(stage)
		if err != nil {
			return err
		}
		if err := live.Apply(ctx, session, compiled); err != nil {
			return fmt.Errorf("variant %s apply failed: %w", item.ThemePublicID+"-"+item.OptionID, err)
		}
		regionReport, err := live.Verify(ctx, session, compiled)
		if err != nil || !validReport(regionReport, compiled) {
			if png, captureErr := live.CapturePNG(ctx, session); captureErr == nil {
				_ = writeExclusive(
					filepath.Join(outputRoot, "failed-"+item.ThemePublicID+"-"+item.OptionID+".png"),
					png,
				)
			}
			return fmt.Errorf(
				"variant %s live verification failed: report=%+v error=%v",
				item.ThemePublicID+"-"+item.OptionID,
				regionReport,
				err,
			)
		}
		png, err := live.CapturePNG(ctx, session)
		if err != nil {
			return err
		}
		screenshotName := item.ThemePublicID + "-" + item.OptionID + ".png"
		if err := writeExclusive(filepath.Join(outputRoot, screenshotName), png); err != nil {
			return err
		}
		report.Variants = append(report.Variants, variantResult{
			ThemePublicID: item.ThemePublicID, Name: item.Name, OptionID: item.OptionID,
			Recommended: item.Recommended, ThemeVersion: item.ThemeVersion, Parameters: item.Parameters,
			PackageSHA256: item.PackageSHA256, Screenshot: screenshotName, Report: regionReport,
		})
		if os.Getenv("CODEX_SKIN_DOM_DIAGNOSTICS") == "1" {
			diagnostics, diagnosticErr := live.FixedLayoutDiagnostics(ctx, session)
			if diagnosticErr != nil {
				return diagnosticErr
			}
			if err := writeJSONExclusive(
				filepath.Join(outputRoot, item.ThemePublicID+"-"+item.OptionID+"-layout.json"),
				diagnostics,
			); err != nil {
				return err
			}
		}
		if err := live.RestoreOfficial(ctx, session); err != nil {
			return err
		}
		if err := live.VerifyOfficial(ctx, session); err != nil {
			return err
		}
	}
	if len(verifiedFinalists) == 3 {
		transactional, err := engine.New(store, &pinnedAdapter{live: live, session: session})
		if err != nil {
			return err
		}
		for _, verified := range verifiedFinalists {
			if _, err := transactional.ApplyVerified(ctx, verified); err != nil {
				return fmt.Errorf("transactional finalist apply failed: %w", err)
			}
			report.Transactions++
		}
		restoreResult, err := transactional.RestoreOfficial(ctx)
		if err != nil || !restoreResult.WasThemed {
			return fmt.Errorf("offline finalist restore failed: result=%+v error=%v", restoreResult, err)
		}
		report.OfflineRestore = true
	}
	report.OfficialRestored = true
	if err := writeJSONExclusive(filepath.Join(outputRoot, "calibration-report.json"), report); err != nil {
		return err
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCancel()
	if err := live.StopOwned(stopCtx, session); err != nil {
		return err
	}
	stopped = true
	fmt.Printf(
		"{\"command\":\"gateb-calibration\",\"variants\":%d,\"version\":%q,\"officialRestored\":true}\n",
		len(report.Variants),
		report.Identity.Version,
	)
	return nil
}

func parseArguments(args []string) (map[string]string, error) {
	if len(args) != 6 && len(args) != 8 {
		return nil, errors.New("expected --index --out --root [--only-theme]")
	}
	values := map[string]string{}
	for index := 0; index < len(args); index += 2 {
		name := args[index]
		if name != "--index" && name != "--out" && name != "--root" &&
			name != "--only-theme" {
			return nil, errors.New("unknown argument")
		}
		if args[index+1] == "" || values[name] != "" {
			return nil, errors.New("invalid argument")
		}
		values[name] = args[index+1]
	}
	return values, nil
}

func selectVariants(index *calibrationIndex, onlyTheme string) error {
	if onlyTheme == "" {
		return nil
	}
	filtered := make([]variant, 0, 1)
	for _, item := range index.Variants {
		if item.ThemePublicID == onlyTheme {
			filtered = append(filtered, item)
		}
	}
	if len(onlyTheme) != 6 || len(filtered) != 1 {
		return errors.New("calibration theme filter is invalid")
	}
	index.Variants = filtered
	return nil
}

func validateVariant(item variant) error {
	if len(item.ThemePublicID) != 6 ||
		(item.OptionID != "clarity" && item.OptionID != "gallery" &&
			item.OptionID != "vivid" && item.OptionID != "immersive" &&
			item.OptionID != "balanced" && item.OptionID != "quiet") ||
		item.Name == "" ||
		item.ThemeVersion == "" ||
		!strings.HasPrefix(item.PackageDirectory, item.ThemePublicID+"-") ||
		len(item.PackageSHA256) != 64 {
		return errors.New("calibration variant is invalid")
	}
	return nil
}

func waitForCoreCapabilities(ctx context.Context, live *adapter.Live, session engine.Session) error {
	for attempts := 0; attempts < 120; attempts++ {
		report, err := live.Probe(ctx, session)
		if err == nil &&
			report.Regions["home"] == engine.RegionPass &&
			report.Regions["sidebar"] == engine.RegionPass &&
			report.Regions["composer"] == engine.RegionPass &&
			report.Regions["composerUtilityBar"] == engine.RegionPass {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return errors.New("Codex core capabilities did not become ready")
}

func validReport(report engine.RegionReport, compiled engine.CompiledTheme) bool {
	if report.StyleMarkerCount != 1 ||
		report.TemplateVersion != compiled.TemplateVersion ||
		report.ThemePublicID != compiled.ThemePublicID ||
		!report.BackgroundLoaded {
		return false
	}
	for _, name := range []string{
		"home", "mainBoundary", "sidebar", "composer", "composerUtilityBar", "topFade",
	} {
		if report.Regions[name] != engine.RegionPass {
			return false
		}
	}
	for _, name := range []string{"suggestionCards", "projectPicker"} {
		if report.Regions[name] != engine.RegionPass && report.Regions[name] != engine.RegionNotPresent {
			return false
		}
	}
	return true
}

func readStrictJSON(path string, limit int64, target any) error {
	content, err := readLimited(path, limit)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Size() < 1 || info.Size() > limit {
		return nil, errors.New("input file is unsafe")
	}
	return os.ReadFile(path)
}

func requireContained(root, candidate string) error {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path escapes calibration root")
	}
	return nil
}

func writeExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func writeJSONExclusive(path string, value any) error {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(path, append(content, '\n'))
}
