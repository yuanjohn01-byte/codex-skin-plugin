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
	"time"
)

const (
	pollPath   = "/api/v1/plugin/device-authorizations/token"
	cancelPath = "/api/v1/plugin/device-authorizations/cancel"

	codeInvalidRequest = "CS-AUTH-POLL-001"
	codeInvalidGrant   = "CS-AUTH-POLL-002"
	codePending        = "CS-AUTH-POLL-003"
	codeSlowDown       = "CS-AUTH-POLL-004"
	codeExpired        = "CS-AUTH-POLL-005"
	codeDenied         = "CS-AUTH-POLL-006"
	codeConsumed       = "CS-AUTH-POLL-007"

	minimumPollInterval = 4 * time.Second
	maximumPollInterval = 30 * time.Second
	maxResponseBytes    = 64 * 1024
)

var (
	ErrInvalidConfiguration = errors.New("device authorization client configuration is invalid")
	ErrInvalidCredentials   = errors.New("device authorization credentials are invalid")
	ErrNetwork              = errors.New("device authorization request failed")
	ErrProtocol             = errors.New("device authorization response is invalid")
)

type Outcome string

const (
	OutcomeAuthorized Outcome = "authorized"
	OutcomeCancelled  Outcome = "cancelled"
	OutcomeExpired    Outcome = "expired"
	OutcomeConsumed   Outcome = "consumed"
	OutcomeInvalid    Outcome = "invalid"
)

type Credentials struct {
	DeviceCode   string
	CodeVerifier string
}

type Result struct {
	Outcome   Outcome
	RequestID string
}

type proofRequest struct {
	DeviceCode   string `json:"deviceCode"`
	CodeVerifier string `json:"codeVerifier"`
}

type responseEnvelope struct {
	RequestID string `json:"requestId"`
	Error     *struct {
		Code    string `json:"code"`
		Details struct {
			RetryAfter int `json:"retryAfter"`
		} `json:"details"`
	} `json:"error"`
}

type waitFunc func(context.Context, time.Duration) error

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	wait       waitFunc
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
	return &Client{
		baseURL:    parsed,
		httpClient: &clientCopy,
		wait:       waitForContext,
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

func validCredentials(credentials Credentials) bool {
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
	if !validCredentials(credentials) || initialInterval < minimumPollInterval {
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
		status, envelope, retryAfter, err := client.post(ctx, pollPath, credentials)
		if err != nil {
			return Result{}, err
		}
		if status == http.StatusOK {
			return Result{Outcome: OutcomeAuthorized, RequestID: envelope.RequestID}, nil
		}
		if envelope.Error == nil {
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

func (client *Client) Cancel(ctx context.Context, credentials Credentials) (Result, error) {
	if !validCredentials(credentials) {
		return Result{}, ErrInvalidCredentials
	}
	status, envelope, _, err := client.post(ctx, cancelPath, credentials)
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

func (client *Client) post(ctx context.Context, path string, credentials Credentials) (int, responseEnvelope, time.Duration, error) {
	body, err := json.Marshal(proofRequest(credentials))
	if err != nil {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
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
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil || envelope.RequestID == "" {
		return 0, responseEnvelope{}, 0, ErrProtocol
	}
	retryAfter := retryAfterDuration(response.Header.Get("Retry-After"), envelope)
	return response.StatusCode, envelope, retryAfter, nil
}

func retryAfterDuration(header string, envelope responseEnvelope) time.Duration {
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 1 || seconds > 300 {
		seconds = 0
	}
	if envelope.Error != nil && envelope.Error.Details.RetryAfter > seconds && envelope.Error.Details.RetryAfter <= 300 {
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
