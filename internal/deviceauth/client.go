package deviceauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	OutcomeAuthorized  Outcome = "authorized"
	OutcomeCancelled   Outcome = "cancelled"
	OutcomeExpired     Outcome = "expired"
	OutcomeConsumed    Outcome = "consumed"
	OutcomeInvalid     Outcome = "invalid"
	OutcomeRetry       Outcome = "retry"
	OutcomeReauthorize Outcome = "reauthorize"
	OutcomeFailed      Outcome = "failed"
)

type Credentials struct {
	DeviceCode   string
	CodeVerifier string
	DeviceKey    string
}

type Device struct {
	ID          string
	DisplayName string
}

type AccessToken struct {
	value     string
	expiresIn time.Duration
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
	Outcome     Outcome
	RequestID   string
	AccessToken *AccessToken
	Device      Device
	ErrorCode   string
	RetryAfter  time.Duration
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
		Code    string `json:"code"`
		Details struct {
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
