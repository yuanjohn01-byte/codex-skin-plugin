package deviceauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const stagingContinuationOrigin = "https://codex-skin-staging.yuanjohn01.workers.dev"

type stagingContinuationFixture struct {
	BaseURL        string      `json:"baseUrl"`
	Credentials    Credentials `json:"credentials"`
	SessionCookie  string      `json:"sessionCookie"`
	RevokeDeviceID string      `json:"revokeDeviceId"`
}

func TestStagingSameTaskContinuation(t *testing.T) {
	if os.Getenv("CODEX_SKIN_STAGING_CONTINUATION_E2E") != "staging-only" {
		t.Skip("stateful Staging continuation test was not explicitly enabled")
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, maxResponseBytes+1))
	if err != nil || len(input) == 0 || len(input) > maxResponseBytes {
		t.Fatal("staging continuation fixture input is invalid")
	}
	defer zeroBytes(input)
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var fixture stagingContinuationFixture
	if err := decoder.Decode(&fixture); err != nil || fixture.BaseURL != stagingContinuationOrigin || !validDeviceID(fixture.RevokeDeviceID) || fixture.SessionCookie == "" || len(fixture.SessionCookie) > 8192 || strings.ContainsAny(fixture.SessionCookie, "\r\n") {
		t.Fatal("staging continuation fixture is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatal("staging continuation fixture contains trailing data")
	}

	httpClient := &http.Client{Timeout: 20 * time.Second}
	store := newMemoryCredentialStore()
	client, err := NewClient(fixture.BaseURL, httpClient, store)
	if err != nil {
		t.Fatal("staging continuation client configuration failed")
	}
	originalOperationMarker := "same-task-staging-operation"
	deviceLimitCount := 0
	operationCount := 0
	result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
		Credentials:     fixture.Credentials,
		InitialInterval: minimumPollInterval,
		AwaitDeviceSlot: func(ctx context.Context, limit DeviceLimit) error {
			deviceLimitCount++
			if limit.ManagementURL != stagingContinuationOrigin+"/settings/devices" || limit.RetryAfter < minimumPollInterval {
				return errors.New("staging device-limit metadata was invalid")
			}
			return revokeStagingDevice(ctx, httpClient, fixture.SessionCookie, fixture.RevokeDeviceID)
		},
		Run: func(_ context.Context, authorized Result) error {
			operationCount++
			if originalOperationMarker != "same-task-staging-operation" || authorized.AccessToken == nil || authorized.AccessToken.Value() == "" {
				return errors.New("staging original operation context was not preserved")
			}
			return nil
		},
	})
	if err != nil || result.Outcome != OutcomeAuthorized || deviceLimitCount != 1 || operationCount != 1 || !validDeviceID(result.Device.ID) {
		t.Fatal("staging same-task continuation did not complete")
	}
	credential := store.credential(t, result.Device.ID)
	if !validOpaqueSecret(credential.RefreshToken) || credential.DeviceKey != fixture.Credentials.DeviceKey {
		t.Fatal("staging continuation did not persist the successful credential")
	}
	credential.RefreshToken = ""
	credential.DeviceKey = ""
	if err := store.Delete(context.Background(), result.Device.ID); err != nil {
		t.Fatal("staging continuation credential cleanup failed")
	}
}

func revokeStagingDevice(ctx context.Context, httpClient *http.Client, sessionCookie, deviceID string) error {
	endpoint, err := url.JoinPath(stagingContinuationOrigin, "/api/v1/account/devices", deviceID, "revoke")
	if err != nil {
		return errors.New("staging device revoke request failed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return errors.New("staging device revoke request failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cookie", sessionCookie)
	request.Header.Set("Origin", stagingContinuationOrigin)
	response, err := httpClient.Do(request)
	if err != nil {
		return errors.New("staging device revoke request failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode != http.StatusOK {
		return errors.New("staging device revoke request failed")
	}
	return nil
}
