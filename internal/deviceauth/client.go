package deviceauth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	startPath   = "/api/v1/plugin/device-authorizations"
	pollPath    = "/api/v1/plugin/device-authorizations/token"
	cancelPath  = "/api/v1/plugin/device-authorizations/cancel"
	refreshPath = "/api/v1/plugin/token/refresh"

	codeInvalidRequest = "CS-AUTH-POLL-001"
	codeInvalidGrant   = "CS-AUTH-POLL-002"
	codePending        = "CS-AUTH-POLL-003"
	codeSlowDown       = "CS-AUTH-POLL-004"
	codeExpired        = "CS-AUTH-POLL-005"
	codeDenied         = "CS-AUTH-POLL-006"
	codeConsumed       = "CS-AUTH-POLL-007"
	codeDeviceLimit    = "CS-AUTH-POLL-010"

	codeStartInvalidRequest = "CS-AUTH-START-001"
	codeStartUnavailable    = "CS-AUTH-START-002"
	codeStartConflict       = "CS-AUTH-START-003"
	codeStartLimited        = "CS-AUTH-START-004"
	codeStartFailed         = "CS-AUTH-START-005"

	codeTokenInvalidRequest = "CS-AUTH-TOKEN-001"
	codeTokenInvalidGrant   = "CS-AUTH-TOKEN-002"
	codeTokenExpired        = "CS-AUTH-TOKEN-003"
	codeTokenReplay         = "CS-AUTH-TOKEN-004"
	codeTokenRevoked        = "CS-AUTH-TOKEN-005"
	codeTokenUnavailable    = "CS-AUTH-TOKEN-006"
	codeTokenFailed         = "CS-AUTH-TOKEN-007"

	minimumPollInterval = 4 * time.Second
	maximumPollInterval = 30 * time.Second
	accessTokenLifetime = 15 * time.Minute
	maxResponseBytes    = 64 * 1024
	credentialSchema    = 1
)

var (
	ErrInvalidConfiguration = errors.New("device authorization client configuration is invalid")
	ErrInvalidCredentials   = errors.New("device authorization credentials are invalid")
	ErrCredentialStore      = errors.New("system credential store operation failed")
	ErrNetwork              = errors.New("device authorization request failed")
	ErrProtocol             = errors.New("device authorization response is invalid")
)

type Outcome string

const (
	OutcomeStarted     Outcome = "started"
	OutcomeAuthorized  Outcome = "authorized"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeExpired     Outcome = "expired"
	OutcomeConsumed    Outcome = "consumed"
	OutcomeInvalid     Outcome = "invalid"
	OutcomeDeviceLimit Outcome = "device_limit"
	OutcomeRetry       Outcome = "retry"
	OutcomeReauthorize Outcome = "reauthorize"
	OutcomeFailed      Outcome = "failed"
)

type Credentials struct {
	DeviceCode   string
	CodeVerifier string
	DeviceKey    string
}

type StartInput struct {
	DeviceDisplayName string
	Platform          string
	PluginVersion     string
	EngineVersion     string
}

type StartResult struct {
	Outcome         Outcome
	RequestID       string
	Credentials     Credentials
	VerificationURL string
	ExpiresIn       time.Duration
	Interval        time.Duration
	ErrorCode       string
	RetryAfter      time.Duration
}

func (Credentials) String() string {
	return "[REDACTED]"
}

func (Credentials) GoString() string {
	return "deviceauth.Credentials([REDACTED])"
}

type Device struct {
	ID          string
	DisplayName string
}

type AccessToken struct {
	value     string
	expiresIn time.Duration
}

func NewAccessToken(value string, expiresIn time.Duration) (*AccessToken, error) {
	if !validAccessToken(value) || expiresIn <= 0 || expiresIn > accessTokenLifetime {
		return nil, ErrInvalidCredentials
	}
	return &AccessToken{value: value, expiresIn: expiresIn}, nil
}

func (token *AccessToken) Value() string {
	if token == nil {
		return ""
	}
	return token.value
}

func (token *AccessToken) ExpiresIn() time.Duration {
	if token == nil {
		return 0
	}
	return token.expiresIn
}

func (*AccessToken) String() string {
	return "[REDACTED]"
}

func (*AccessToken) GoString() string {
	return "deviceauth.AccessToken([REDACTED])"
}

type Result struct {
	Outcome       Outcome
	RequestID     string
	AccessToken   *AccessToken
	Device        Device
	ErrorCode     string
	RetryAfter    time.Duration
	ManagementURL string
}

type DeviceLimit struct {
	RequestID     string
	RetryAfter    time.Duration
	ManagementURL string
}

type Continuation struct {
	Credentials     Credentials
	InitialInterval time.Duration
	AwaitDeviceSlot func(context.Context, DeviceLimit) error
	Run             func(context.Context, Result) error
}

func (Continuation) String() string {
	return "[REDACTED]"
}

func (Continuation) GoString() string {
	return "deviceauth.Continuation([REDACTED])"
}

type CredentialStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

type proofRequest struct {
	DeviceCode   string `json:"deviceCode"`
	CodeVerifier string `json:"codeVerifier"`
}

type refreshRequest struct {
	RefreshToken string `json:"refreshToken"`
	DeviceKey    string `json:"deviceKey"`
}

type tokenResponseData struct {
	TokenType    string `json:"tokenType"`
	AccessToken  string `json:"accessToken"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken"`
	Device       struct {
		ID          string `json:"id"`
		DisplayName string `json:"displayName"`
	} `json:"device"`
}

type responseEnvelope struct {
	Data      *tokenResponseData `json:"data"`
	RequestID string             `json:"requestId"`
	Error     *struct {
		Code      string `json:"code"`
		Action    string `json:"action"`
		Retryable bool   `json:"retryable"`
		Details   struct {
			State         string `json:"state"`
			RetryAfter    int    `json:"retryAfter"`
			ManagementURL string `json:"managementUrl"`
		} `json:"details"`
	} `json:"error"`
}

type startRequest struct {
	DeviceKey             string `json:"deviceKey"`
	DeviceDisplayName     string `json:"deviceDisplayName"`
	Platform              string `json:"platform"`
	CodeChallenge         string `json:"codeChallenge"`
	PluginVersion         string `json:"pluginVersion"`
	EngineVersion         string `json:"engineVersion"`
	PluginProtocolVersion int    `json:"pluginProtocolVersion"`
}

type startResponseEnvelope struct {
	Data *struct {
		DeviceCode              string `json:"deviceCode"`
		VerificationURIComplete string `json:"verificationUriComplete"`
		ExpiresIn               int    `json:"expiresIn"`
		Interval                int    `json:"interval"`
	} `json:"data"`
	RequestID string `json:"requestId"`
	Error     *struct {
		Code       string `json:"code"`
		Message    string `json:"message"`
		Action     string `json:"action"`
		Retryable  bool   `json:"retryable"`
		IncidentID any    `json:"incidentId"`
		Details    struct {
			RetryAfter int `json:"retryAfter"`
		} `json:"details"`
	} `json:"error"`
}

type storedCredential struct {
	SchemaVersion int    `json:"schemaVersion"`
	RefreshToken  string `json:"refreshToken"`
	DeviceKey     string `json:"deviceKey"`
}

type waitFunc func(context.Context, time.Duration) error

type Client struct {
	baseURL         *url.URL
	httpClient      *http.Client
	credentialStore CredentialStore
	refreshMu       sync.Mutex
	wait            waitFunc
}

func NewClient(baseURL string, httpClient *http.Client, credentialStore CredentialStore) (*Client, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || !validBaseURL(parsed) || credentialStore == nil {
		return nil, ErrInvalidConfiguration
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		baseURL:         parsed,
		httpClient:      &clientCopy,
		credentialStore: credentialStore,
		wait:            waitForContext,
	}, nil
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

func validProofCredentials(credentials Credentials) bool {
	if len(credentials.DeviceCode) != 43 || len(credentials.CodeVerifier) < 43 || len(credentials.CodeVerifier) > 128 {
		return false
	}
	for _, value := range []string{credentials.DeviceCode, credentials.CodeVerifier} {
		for _, character := range value {
			if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~", character) {
				return false
			}
		}
	}
	return !strings.ContainsAny(credentials.DeviceCode, ".~")
}

func validOpaqueSecret(value string) bool {
	if len(value) != 43 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", character) {
			return false
		}
	}
	return true
}

func validDeviceID(value string) bool {
	return len(value) == 36 && strings.HasPrefix(value, "dev_") && validBase64URL(value[4:])
}

func validBase64URL(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", character) {
			return false
		}
	}
	return true
}

func validAccessToken(value string) bool {
	if len(value) < 80 || len(value) > 8192 {
		return false
	}
	parts := strings.Split(value, ".")
	return len(parts) == 3 && validBase64URL(parts[0]) && validBase64URL(parts[1]) && validBase64URL(parts[2])
}

func validTokenData(data *tokenResponseData) bool {
	if data == nil || data.TokenType != "Bearer" || data.ExpiresIn != int(accessTokenLifetime/time.Second) {
		return false
	}
	if !validAccessToken(data.AccessToken) || !validOpaqueSecret(data.RefreshToken) || !validDeviceID(data.Device.ID) {
		return false
	}
	nameLength := utf8.RuneCountInString(data.Device.DisplayName)
	return utf8.ValidString(data.Device.DisplayName) && nameLength >= 1 && nameLength <= 80
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

func (client *Client) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if !validStartInput(input) {
		return StartResult{}, ErrInvalidConfiguration
	}
	deviceKey, err := randomSecret()
	if err != nil {
		return StartResult{}, ErrInvalidConfiguration
	}
	codeVerifier, err := randomSecret()
	if err != nil {
		return StartResult{}, ErrInvalidConfiguration
	}
	challengeDigest := sha256.Sum256([]byte(codeVerifier))
	idempotencyKey, err := randomUUIDv4()
	if err != nil {
		return StartResult{}, ErrInvalidConfiguration
	}
	payload := startRequest{
		DeviceKey:             deviceKey,
		DeviceDisplayName:     input.DeviceDisplayName,
		Platform:              input.Platform,
		CodeChallenge:         base64.RawURLEncoding.EncodeToString(challengeDigest[:]),
		PluginVersion:         input.PluginVersion,
		EngineVersion:         input.EngineVersion,
		PluginProtocolVersion: 1,
	}
	status, envelope, retryAfter, replayed, err := client.postStart(ctx, idempotencyKey, payload)
	if err != nil {
		return StartResult{}, err
	}
	if status == http.StatusCreated || status == http.StatusOK {
		if envelope.Error != nil ||
			envelope.Data == nil ||
			(status == http.StatusOK && !replayed) ||
			(status == http.StatusCreated && replayed) ||
			!validOpaqueSecret(envelope.Data.DeviceCode) ||
			envelope.Data.ExpiresIn < 1 ||
			envelope.Data.ExpiresIn > 300 ||
			envelope.Data.Interval != int(minimumPollInterval/time.Second) ||
			!client.validVerificationURL(envelope.Data.VerificationURIComplete) {
			return StartResult{}, ErrProtocol
		}
		return StartResult{
			Outcome:         OutcomeStarted,
			RequestID:       envelope.RequestID,
			Credentials:     Credentials{DeviceCode: envelope.Data.DeviceCode, CodeVerifier: codeVerifier, DeviceKey: deviceKey},
			VerificationURL: envelope.Data.VerificationURIComplete,
			ExpiresIn:       time.Duration(envelope.Data.ExpiresIn) * time.Second,
			Interval:        time.Duration(envelope.Data.Interval) * time.Second,
		}, nil
	}
	if envelope.Error == nil || envelope.Data != nil {
		return StartResult{}, ErrProtocol
	}
	result := StartResult{
		RequestID:  envelope.RequestID,
		ErrorCode:  envelope.Error.Code,
		RetryAfter: retryAfter,
	}
	switch envelope.Error.Code {
	case codeStartUnavailable, codeStartLimited:
		if (status != http.StatusServiceUnavailable && status != http.StatusTooManyRequests) ||
			!envelope.Error.Retryable ||
			envelope.Error.Action != "retry_later" {
			return StartResult{}, ErrProtocol
		}
		result.Outcome = OutcomeRetry
		return result, nil
	case codeStartConflict:
		if (status != http.StatusConflict && status != http.StatusGone) ||
			envelope.Error.Action != "use_new_idempotency_key" {
			return StartResult{}, ErrProtocol
		}
		result.Outcome = OutcomeInvalid
		return result, nil
	case codeStartInvalidRequest, codeStartFailed:
		return StartResult{}, ErrProtocol
	default:
		return StartResult{}, ErrProtocol
	}
}

func validStartInput(input StartInput) bool {
	nameLength := utf8.RuneCountInString(input.DeviceDisplayName)
	if !utf8.ValidString(input.DeviceDisplayName) ||
		strings.TrimSpace(input.DeviceDisplayName) != input.DeviceDisplayName ||
		nameLength < 1 ||
		nameLength > 80 ||
		(input.Platform != "macos" && input.Platform != "windows") ||
		!validSemver(input.PluginVersion) ||
		!validSemver(input.EngineVersion) {
		return false
	}
	for _, character := range input.DeviceDisplayName {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validSemver(value string) bool {
	if len(value) < 5 || len(value) > 64 || strings.Contains(value, "+") {
		return false
	}
	parts := strings.SplitN(value, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return false
	}
	for _, part := range core {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	if len(parts) == 2 && (parts[1] == "" || strings.ContainsAny(parts[1], "_+")) {
		return false
	}
	return true
}

func randomSecret() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func randomUUIDv4() (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}

func (client *Client) validVerificationURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil ||
		!parsed.IsAbs() ||
		parsed.User != nil ||
		parsed.Path != "/device/approve" ||
		parsed.RawPath != "" ||
		parsed.Fragment != "" ||
		parsed.Query().Get("token") == "" ||
		len(parsed.Query()) != 1 ||
		!validOpaqueSecret(parsed.Query().Get("token")) {
		return false
	}
	return strings.EqualFold(parsed.Scheme, client.baseURL.Scheme) &&
		strings.EqualFold(parsed.Hostname(), client.baseURL.Hostname()) &&
		parsed.Port() == client.baseURL.Port()
}

func (client *Client) Poll(ctx context.Context, credentials Credentials, initialInterval time.Duration) (Result, error) {
	if !validProofCredentials(credentials) || !validOpaqueSecret(credentials.DeviceKey) || initialInterval < minimumPollInterval {
		return Result{}, ErrInvalidCredentials
	}
	interval := initialInterval
	if interval > maximumPollInterval {
		interval = maximumPollInterval
	}

	for {
		if err := client.wait(ctx, interval); err != nil {
			return Result{}, err
		}
		status, envelope, retryAfter, err := client.post(ctx, pollPath, proofRequest{
			DeviceCode:   credentials.DeviceCode,
			CodeVerifier: credentials.CodeVerifier,
		})
		if err != nil {
			return Result{}, err
		}
		if status == http.StatusOK {
			if envelope.Error != nil || !validTokenData(envelope.Data) {
				return Result{}, ErrProtocol
			}
			if err := client.persistCredential(ctx, envelope.Data.Device.ID, envelope.Data.RefreshToken, credentials.DeviceKey); err != nil {
				return Result{}, err
			}
			return authorizedResult(envelope), nil
		}
		if envelope.Error == nil || envelope.Data != nil {
			return Result{}, ErrProtocol
		}
		switch envelope.Error.Code {
		case codePending:
			if status != http.StatusAccepted {
				return Result{}, ErrProtocol
			}
			interval = largerDuration(interval, retryAfter)
		case codeSlowDown:
			if status != http.StatusTooManyRequests {
				return Result{}, ErrProtocol
			}
			interval = largerDuration(interval+5*time.Second, retryAfter)
		case codeDenied:
			if status != http.StatusForbidden {
				return Result{}, ErrProtocol
			}
			return Result{Outcome: OutcomeCancelled, RequestID: envelope.RequestID}, nil
		case codeExpired:
			if status != http.StatusGone {
				return Result{}, ErrProtocol
			}
			return Result{Outcome: OutcomeExpired, RequestID: envelope.RequestID}, nil
		case codeConsumed:
			if status != http.StatusConflict {
				return Result{}, ErrProtocol
			}
			return Result{Outcome: OutcomeConsumed, RequestID: envelope.RequestID}, nil
		case codeDeviceLimit:
			if status != http.StatusConflict || !client.validDeviceLimit(envelope, retryAfter) {
				return Result{}, ErrProtocol
			}
			return Result{
				Outcome:       OutcomeDeviceLimit,
				RequestID:     envelope.RequestID,
				ErrorCode:     codeDeviceLimit,
				RetryAfter:    retryAfter,
				ManagementURL: envelope.Error.Details.ManagementURL,
			}, nil
		case codeInvalidRequest, codeInvalidGrant:
			if status != http.StatusBadRequest {
				return Result{}, ErrProtocol
			}
			return Result{Outcome: OutcomeInvalid, RequestID: envelope.RequestID}, nil
		default:
			return Result{}, ErrProtocol
		}
		if interval > maximumPollInterval {
			interval = maximumPollInterval
		}
	}
}

func (client *Client) AuthorizeAndContinue(ctx context.Context, continuation Continuation) (Result, error) {
	if continuation.Run == nil {
		return Result{}, ErrInvalidConfiguration
	}
	interval := continuation.InitialInterval
	deviceLimitHandled := false
	for {
		result, err := client.Poll(ctx, continuation.Credentials, interval)
		if err != nil {
			return Result{}, err
		}
		if result.Outcome == OutcomeDeviceLimit {
			if continuation.AwaitDeviceSlot == nil || deviceLimitHandled {
				return result, nil
			}
			if err := continuation.AwaitDeviceSlot(ctx, DeviceLimit{
				RequestID:     result.RequestID,
				RetryAfter:    result.RetryAfter,
				ManagementURL: result.ManagementURL,
			}); err != nil {
				return result, err
			}
			deviceLimitHandled = true
			interval = largerDuration(interval, result.RetryAfter)
			continue
		}
		if result.Outcome == OutcomeAuthorized {
			if err := continuation.Run(ctx, result); err != nil {
				return result, err
			}
		}
		return result, nil
	}
}

func (client *Client) validDeviceLimit(envelope responseEnvelope, retryAfter time.Duration) bool {
	if envelope.Error == nil || envelope.Error.Action != "manage_devices" || !envelope.Error.Retryable || envelope.Error.Details.State != "device_limit_reached" || retryAfter <= 0 {
		return false
	}
	managementURL, err := url.Parse(envelope.Error.Details.ManagementURL)
	if err != nil || !managementURL.IsAbs() || managementURL.Opaque != "" || managementURL.User != nil || managementURL.RawQuery != "" || managementURL.ForceQuery || managementURL.Fragment != "" || managementURL.RawPath != "" || managementURL.Path != "/settings/devices" {
		return false
	}
	return strings.EqualFold(managementURL.Scheme, client.baseURL.Scheme) &&
		strings.EqualFold(managementURL.Hostname(), client.baseURL.Hostname()) &&
		managementURL.Port() == client.baseURL.Port()
}

func (client *Client) Refresh(ctx context.Context, deviceID string) (Result, error) {
	if !validDeviceID(deviceID) {
		return Result{}, ErrInvalidCredentials
	}
	client.refreshMu.Lock()
	defer client.refreshMu.Unlock()
	credential, err := client.loadCredential(ctx, deviceID)
	if err != nil {
		return Result{}, err
	}
	status, envelope, retryAfter, err := client.post(ctx, refreshPath, refreshRequest{
		RefreshToken: credential.RefreshToken,
		DeviceKey:    credential.DeviceKey,
	})
	credential.RefreshToken = ""
	if err != nil {
		return Result{}, err
	}
	if status == http.StatusOK {
		if envelope.Error != nil || !validTokenData(envelope.Data) || envelope.Data.Device.ID != deviceID {
			return Result{}, ErrProtocol
		}
		if err := client.persistCredential(ctx, deviceID, envelope.Data.RefreshToken, credential.DeviceKey); err != nil {
			return Result{}, err
		}
		return authorizedResult(envelope), nil
	}
	if envelope.Error == nil || envelope.Data != nil {
		return Result{}, ErrProtocol
	}
	switch envelope.Error.Code {
	case codeTokenInvalidGrant, codeTokenExpired, codeTokenReplay, codeTokenRevoked:
		if status != http.StatusUnauthorized {
			return Result{}, ErrProtocol
		}
		if err := client.deleteCredential(ctx, deviceID); err != nil {
			return Result{}, err
		}
		return Result{
			Outcome:   OutcomeReauthorize,
			RequestID: envelope.RequestID,
			ErrorCode: envelope.Error.Code,
		}, nil
	case codeTokenUnavailable:
		if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
			return Result{}, ErrProtocol
		}
		return Result{
			Outcome:    OutcomeRetry,
			RequestID:  envelope.RequestID,
			ErrorCode:  envelope.Error.Code,
			RetryAfter: retryAfter,
		}, nil
	case codeTokenFailed:
		if status != http.StatusInternalServerError {
			return Result{}, ErrProtocol
		}
		return Result{Outcome: OutcomeFailed, RequestID: envelope.RequestID, ErrorCode: envelope.Error.Code}, nil
	case codeTokenInvalidRequest:
		return Result{}, ErrProtocol
	default:
		return Result{}, ErrProtocol
	}
}

func (client *Client) Cancel(ctx context.Context, credentials Credentials) (Result, error) {
	if !validProofCredentials(credentials) {
		return Result{}, ErrInvalidCredentials
	}
	status, envelope, _, err := client.post(ctx, cancelPath, proofRequest{
		DeviceCode:   credentials.DeviceCode,
		CodeVerifier: credentials.CodeVerifier,
	})
	if err != nil {
		return Result{}, err
	}
	if status == http.StatusOK && envelope.Error == nil {
		return Result{Outcome: OutcomeCancelled, RequestID: envelope.RequestID}, nil
	}
	if envelope.Error == nil {
		return Result{}, ErrProtocol
	}
	switch envelope.Error.Code {
	case codeExpired:
		if status != http.StatusGone {
			return Result{}, ErrProtocol
		}
		return Result{Outcome: OutcomeExpired, RequestID: envelope.RequestID}, nil
	case codeConsumed:
		if status != http.StatusConflict {
			return Result{}, ErrProtocol
		}
		return Result{Outcome: OutcomeConsumed, RequestID: envelope.RequestID}, nil
	case codeInvalidRequest, codeInvalidGrant:
		if status != http.StatusBadRequest {
			return Result{}, ErrProtocol
		}
		return Result{Outcome: OutcomeInvalid, RequestID: envelope.RequestID}, nil
	default:
		return Result{}, ErrProtocol
	}
}

func authorizedResult(envelope responseEnvelope) Result {
	return Result{
		Outcome:   OutcomeAuthorized,
		RequestID: envelope.RequestID,
		AccessToken: &AccessToken{
			value:     envelope.Data.AccessToken,
			expiresIn: accessTokenLifetime,
		},
		Device: Device{
			ID:          envelope.Data.Device.ID,
			DisplayName: envelope.Data.Device.DisplayName,
		},
	}
}

func (client *Client) persistCredential(ctx context.Context, deviceID, refreshToken, deviceKey string) error {
	credential := storedCredential{
		SchemaVersion: credentialSchema,
		RefreshToken:  refreshToken,
		DeviceKey:     deviceKey,
	}
	payload, err := json.Marshal(credential)
	credential.RefreshToken = ""
	credential.DeviceKey = ""
	if err != nil {
		return ErrCredentialStore
	}
	defer zeroBytes(payload)
	if err := client.credentialStore.Put(ctx, deviceID, payload); err != nil {
		return ErrCredentialStore
	}
	return nil
}

func (client *Client) loadCredential(ctx context.Context, deviceID string) (storedCredential, error) {
	payload, err := client.credentialStore.Get(ctx, deviceID)
	if err != nil {
		return storedCredential{}, ErrCredentialStore
	}
	defer zeroBytes(payload)
	var credential storedCredential
	if err := json.Unmarshal(payload, &credential); err != nil || credential.SchemaVersion != credentialSchema || !validOpaqueSecret(credential.RefreshToken) || !validOpaqueSecret(credential.DeviceKey) {
		credential.RefreshToken = ""
		credential.DeviceKey = ""
		return storedCredential{}, ErrCredentialStore
	}
	return credential, nil
}

func (client *Client) deleteCredential(ctx context.Context, deviceID string) error {
	if err := client.credentialStore.Delete(ctx, deviceID); err != nil {
		return ErrCredentialStore
	}
	return nil
}

func (client *Client) postStart(ctx context.Context, idempotencyKey string, payload startRequest) (int, startResponseEnvelope, time.Duration, bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	defer zeroBytes(body)
	endpoint := client.baseURL.ResolveReference(&url.URL{Path: startPath})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, startResponseEnvelope{}, 0, false, ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes {
		zeroBytes(responseBody)
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	defer zeroBytes(responseBody)
	var envelope startResponseEnvelope
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil || !validRequestID(envelope.RequestID) {
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return 0, startResponseEnvelope{}, 0, false, ErrProtocol
	}
	replayed := response.Header.Get("Idempotency-Replayed") == "true"
	retryAfter := time.Duration(0)
	if envelope.Error != nil {
		seconds, parseErr := strconv.Atoi(response.Header.Get("Retry-After"))
		if parseErr == nil && seconds >= 1 && seconds <= 600 {
			retryAfter = time.Duration(seconds) * time.Second
		}
		if envelope.Error.Details.RetryAfter > 0 &&
			envelope.Error.Details.RetryAfter <= 600 &&
			time.Duration(envelope.Error.Details.RetryAfter)*time.Second > retryAfter {
			retryAfter = time.Duration(envelope.Error.Details.RetryAfter) * time.Second
		}
	}
	return response.StatusCode, envelope, retryAfter, replayed, nil
}

func (client *Client) post(ctx context.Context, path string, payload any) (int, responseEnvelope, time.Duration, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	defer zeroBytes(body)
	endpoint := client.baseURL.ResolveReference(&url.URL{Path: path})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return 0, responseEnvelope{}, 0, ErrNetwork
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil || len(responseBody) > maxResponseBytes {
		zeroBytes(responseBody)
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	defer zeroBytes(responseBody)
	var envelope responseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil || !validRequestID(envelope.RequestID) {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	retryAfter := retryAfterDuration(response.Header.Get("Retry-After"), envelope)
	return response.StatusCode, envelope, retryAfter, nil
}

func validRequestID(value string) bool {
	if len(value) != 36 || !strings.HasPrefix(value, "req_") {
		return false
	}
	for _, character := range value[4:] {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func retryAfterDuration(header string, envelope responseEnvelope) time.Duration {
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 1 || seconds > 600 {
		seconds = 0
	}
	if envelope.Error != nil && envelope.Error.Details.RetryAfter > seconds && envelope.Error.Details.RetryAfter <= 600 {
		seconds = envelope.Error.Details.RetryAfter
	}
	return time.Duration(seconds) * time.Second
}

func largerDuration(left, right time.Duration) time.Duration {
	if right > left {
		return right
	}
	return left
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
