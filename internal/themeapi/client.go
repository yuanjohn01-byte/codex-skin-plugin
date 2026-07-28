// Package themeapi consumes the exported Private theme release/download
// contract without trusting server-provided filesystem paths or redirects.
package themeapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

const (
	protocolVersion  = 1
	maxResponseBytes = 64 * 1024
)

var (
	ErrInvalidConfiguration = errors.New("theme API client configuration is invalid")
	ErrInvalidRequest       = errors.New("theme API request is invalid")
	ErrAuthorization        = errors.New("theme API authorization is required")
	ErrAccessRequired       = errors.New("theme requires Pro access")
	ErrUnavailable          = errors.New("theme release is unavailable")
	ErrNetwork              = errors.New("theme API request failed")
	ErrProtocol             = errors.New("theme API response is invalid")
	ErrDownload             = errors.New("theme package download failed")

	publicIDPattern   = regexp.MustCompile(`^[0-9]{6}$`)
	requestIDPattern  = regexp.MustCompile(`^req_[0-9a-f]{32}$`)
	incidentIDPattern = regexp.MustCompile(`^INC-[A-F0-9]{12}$`)
	semverPattern     = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
)

type Outcome string

const (
	OutcomeReady          Outcome = "ready"
	OutcomeReauthorize    Outcome = "reauthorize"
	OutcomeAccessRequired Outcome = "access_required"
	OutcomeUnavailable    Outcome = "unavailable"
	OutcomeRetry          Outcome = "retry"
)

type Release struct {
	ThemePublicID    string
	ThemeVersion     string
	Tier             string
	Descriptor       theme.Descriptor
	DescriptorBytes  []byte
	SignatureBytes   []byte
	MinEngineVersion string
	DownloadPath     string
}

type Result struct {
	Outcome     Outcome
	RequestID   string
	Release     *Release
	ErrorCode   string
	PricingPath string
	IncidentID  string
}

type successEnvelope struct {
	Data struct {
		ThemePublicID     string           `json:"themePublicId"`
		ThemeVersion      string           `json:"themeVersion"`
		Tier              string           `json:"tier"`
		ReleaseDescriptor theme.Descriptor `json:"releaseDescriptor"`
		Signature         string           `json:"signature"`
		MinEngineVersion  string           `json:"minEngineVersion"`
		DownloadPath      string           `json:"downloadPath"`
	} `json:"data"`
	RequestID string `json:"requestId"`
}

type errorEnvelope struct {
	Error struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Action     string `json:"action"`
		Retryable  bool   `json:"retryable"`
		IncidentID any    `json:"incidentId"`
		Details    struct {
			ThemePublicID string `json:"themePublicId"`
			ThemeVersion  string `json:"themeVersion"`
			PricingPath   string `json:"pricingPath"`
		} `json:"details"`
	} `json:"error"`
	RequestID string `json:"requestId"`
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

func NewClient(baseURL string, httpClient *http.Client) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || !validBaseURL(parsed) {
		return nil, ErrInvalidConfiguration
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{baseURL: parsed, httpClient: &clientCopy}, nil
}

func validBaseURL(value *url.URL) bool {
	if value == nil || value.User != nil || value.RawQuery != "" || value.Fragment != "" || value.Path != "" {
		return false
	}
	if value.Scheme == "https" && value.Hostname() != "" {
		return true
	}
	if value.Scheme != "http" {
		return false
	}
	host := value.Hostname()
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

func (client *Client) Metadata(ctx context.Context, themePublicID, accessToken string) (Result, error) {
	if !publicIDPattern.MatchString(themePublicID) || !validAccessToken(accessToken) {
		return Result{}, ErrInvalidRequest
	}
	endpoint := client.baseURL.ResolveReference(&url.URL{
		Path: "/api/v1/plugin/themes/" + themePublicID,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Result{}, ErrInvalidRequest
	}
	setHeaders(request, accessToken)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return Result{}, ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return Result{}, ErrProtocol
	}
	raw, err := readBounded(response.Body, maxResponseBytes)
	if err != nil {
		return Result{}, ErrProtocol
	}
	if response.StatusCode == http.StatusOK {
		var envelope successEnvelope
		if err := decodeStrict(raw, &envelope); err != nil {
			return Result{}, ErrProtocol
		}
		release, err := validateRelease(client.baseURL, themePublicID, envelope)
		if err != nil {
			return Result{}, err
		}
		return Result{
			Outcome:   OutcomeReady,
			RequestID: envelope.RequestID,
			Release:   &release,
		}, nil
	}

	var envelope errorEnvelope
	if err := decodeStrict(raw, &envelope); err != nil || !requestIDPattern.MatchString(envelope.RequestID) {
		return Result{}, ErrProtocol
	}
	if envelope.Error.Message == "" ||
		len(envelope.Error.Message) > 256 ||
		envelope.Error.Code == "" {
		return Result{}, ErrProtocol
	}
	result := Result{
		RequestID: envelope.RequestID,
		ErrorCode: envelope.Error.Code,
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized &&
		envelope.Error.Code == "CS-THEME-RELEASE-002" &&
		envelope.Error.Action == "reauthorize" &&
		!envelope.Error.Retryable &&
		envelope.Error.IncidentID == nil:
		result.Outcome = OutcomeReauthorize
		return result, nil
	case response.StatusCode == http.StatusForbidden &&
		envelope.Error.Code == "CS-THEME-RELEASE-003" &&
		envelope.Error.Action == "purchase_access" &&
		!envelope.Error.Retryable &&
		envelope.Error.IncidentID == nil:
		if envelope.Error.Details.ThemePublicID != themePublicID ||
			!semverPattern.MatchString(envelope.Error.Details.ThemeVersion) ||
			!validPricingPath(envelope.Error.Details.PricingPath, themePublicID) {
			return Result{}, ErrProtocol
		}
		result.Outcome = OutcomeAccessRequired
		result.PricingPath = envelope.Error.Details.PricingPath
		return result, nil
	case (response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound) &&
		envelope.Error.Code == "CS-THEME-RELEASE-004" &&
		envelope.Error.Action == "choose_available_theme" &&
		!envelope.Error.Retryable &&
		envelope.Error.IncidentID == nil:
		result.Outcome = OutcomeUnavailable
		return result, nil
	case response.StatusCode == http.StatusInternalServerError &&
		envelope.Error.Code == "CS-THEME-RELEASE-005" &&
		envelope.Error.Action == "retry_or_contact_support" &&
		envelope.Error.Retryable:
		incident, ok := envelope.Error.IncidentID.(string)
		if !ok || !incidentIDPattern.MatchString(incident) {
			return Result{}, ErrProtocol
		}
		result.Outcome = OutcomeRetry
		result.IncidentID = incident
		return result, nil
	default:
		return Result{}, ErrProtocol
	}
}

func validateRelease(baseURL *url.URL, requestedID string, envelope successEnvelope) (Release, error) {
	data := envelope.Data
	if !requestIDPattern.MatchString(envelope.RequestID) ||
		data.ThemePublicID != requestedID ||
		data.ReleaseDescriptor.ThemePublicID != requestedID ||
		data.ThemeVersion != data.ReleaseDescriptor.ThemeVersion ||
		(data.Tier != "free" && data.Tier != "pro") ||
		data.MinEngineVersion == "" ||
		len(data.MinEngineVersion) > 64 ||
		data.DownloadPath != "/api/v1/plugin/themes/"+requestedID+"/download" {
		return Release{}, ErrProtocol
	}
	descriptorBytes, err := json.Marshal(data.ReleaseDescriptor)
	if err != nil {
		return Release{}, ErrProtocol
	}
	descriptorBytes = append(descriptorBytes, '\n')
	descriptor, err := theme.ParseDescriptor(descriptorBytes)
	if err != nil {
		return Release{}, ErrProtocol
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(data.Signature)
	if err != nil || len(signature) != 64 {
		return Release{}, ErrProtocol
	}
	downloadURL := baseURL.ResolveReference(&url.URL{Path: data.DownloadPath})
	if !strings.EqualFold(downloadURL.Scheme, baseURL.Scheme) ||
		!strings.EqualFold(downloadURL.Hostname(), baseURL.Hostname()) ||
		downloadURL.Port() != baseURL.Port() {
		return Release{}, ErrProtocol
	}
	return Release{
		ThemePublicID:    data.ThemePublicID,
		ThemeVersion:     data.ThemeVersion,
		Tier:             data.Tier,
		Descriptor:       descriptor,
		DescriptorBytes:  descriptorBytes,
		SignatureBytes:   append([]byte(data.Signature), '\n'),
		MinEngineVersion: data.MinEngineVersion,
		DownloadPath:     data.DownloadPath,
	}, nil
}

func (client *Client) Download(ctx context.Context, release Release, accessToken, destination string) error {
	if !validAccessToken(accessToken) ||
		!publicIDPattern.MatchString(release.ThemePublicID) ||
		release.DownloadPath != "/api/v1/plugin/themes/"+release.ThemePublicID+"/download" ||
		release.Descriptor.ThemePublicID != release.ThemePublicID ||
		release.Descriptor.ThemeVersion != release.ThemeVersion ||
		release.Descriptor.PackageByteSize < 1 ||
		release.Descriptor.PackageByteSize > theme.MaxPackageBytes ||
		!filepath.IsAbs(destination) ||
		filepath.Ext(destination) != ".part" {
		return ErrInvalidRequest
	}
	body, err := json.Marshal(struct {
		ThemeVersion string `json:"themeVersion"`
	}{ThemeVersion: release.ThemeVersion})
	if err != nil {
		return ErrInvalidRequest
	}
	endpoint := client.baseURL.ResolveReference(&url.URL{Path: release.DownloadPath})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return ErrInvalidRequest
	}
	setHeaders(request, accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "application/octet-stream" ||
		response.Header.Get("Content-Encoding") != "" {
		return ErrDownload
	}
	if contentLength := response.Header.Get("Content-Length"); contentLength != "" {
		size, parseErr := strconv.ParseInt(contentLength, 10, 64)
		if parseErr != nil || size != release.Descriptor.PackageByteSize {
			return ErrDownload
		}
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ErrDownload
	}
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(destination)
		}
	}()
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, release.Descriptor.PackageByteSize+1))
	if copyErr != nil || written != release.Descriptor.PackageByteSize {
		return ErrDownload
	}
	extra := make([]byte, 1)
	if count, readErr := response.Body.Read(extra); count != 0 || (readErr != nil && !errors.Is(readErr, io.EOF)) {
		return ErrDownload
	}
	if err := file.Sync(); err != nil {
		return ErrDownload
	}
	if err := file.Close(); err != nil {
		return ErrDownload
	}
	cleanup = false
	return nil
}

func setHeaders(request *http.Request, accessToken string) {
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("X-Codex-Skin-Protocol", strconv.Itoa(protocolVersion))
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || int64(len(raw)) > maximum {
		return nil, ErrProtocol
	}
	return raw, nil
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func validAccessToken(value string) bool {
	if len(value) < 80 || len(value) > 8192 {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, character := range part {
			if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", character) {
				return false
			}
		}
	}
	return true
}

func validPricingPath(value, themePublicID string) bool {
	return value == "/pricing?theme="+themePublicID
}
