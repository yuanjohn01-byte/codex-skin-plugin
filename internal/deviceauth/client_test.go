package deviceauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testDeviceCode = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testVerifier   = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	testDeviceKey  = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	testDeviceID   = "dev_0123456789abcdefghijklmnopqrstuv"
)

type memoryCredentialStore struct {
	mu     sync.Mutex
	values map[string][]byte
	putErr error
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{values: map[string][]byte{}}
}

func (store *memoryCredentialStore) Put(_ context.Context, deviceID string, secret []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.putErr != nil {
		return store.putErr
	}
	store.values[deviceID] = append([]byte(nil), secret...)
	return nil
}

func (store *memoryCredentialStore) Get(_ context.Context, deviceID string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	secret, ok := store.values[deviceID]
	if !ok {
		return nil, errors.New("credential not found")
	}
	return append([]byte(nil), secret...), nil
}

func (store *memoryCredentialStore) Delete(_ context.Context, deviceID string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.values[deviceID]; !ok {
		return errors.New("credential not found")
	}
	delete(store.values, deviceID)
	return nil
}

func (store *memoryCredentialStore) credential(t *testing.T, deviceID string) storedCredential {
	t.Helper()
	payload, err := store.Get(context.Background(), deviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload)
	var credential storedCredential
	if err := json.Unmarshal(payload, &credential); err != nil {
		t.Fatal(err)
	}
	return credential
}

func envelope(code string, retryAfter int) string {
	payload := map[string]any{
		"error": map[string]any{
			"code":       code,
			"message":    "synthetic",
			"action":     "continue_polling",
			"retryable":  true,
			"incidentId": nil,
			"details": map[string]any{
				"retryAfter": retryAfter,
			},
		},
		"requestId": "req_0123456789abcdef0123456789abcdef",
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func deviceLimitEnvelope(action, state string, retryable bool, retryAfter int, managementURL string) string {
	payload := map[string]any{
		"error": map[string]any{
			"code":       codeDeviceLimit,
			"message":    "synthetic",
			"action":     action,
			"retryable":  retryable,
			"incidentId": nil,
			"details": map[string]any{
				"state":         state,
				"retryAfter":    retryAfter,
				"managementUrl": managementURL,
			},
		},
		"requestId": "req_0123456789abcdef0123456789abcdef",
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func TestPollRespectsPendingAndSlowDownIntervals(t *testing.T) {
	responses := []struct {
		status     int
		code       string
		retryAfter int
	}{
		{http.StatusAccepted, codePending, 4},
		{http.StatusTooManyRequests, codeSlowDown, 9},
		{http.StatusAccepted, codePending, 9},
	}
	var mu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != pollPath || request.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		var proof proofRequest
		if err := json.NewDecoder(request.Body).Decode(&proof); err != nil || proof.DeviceCode != testDeviceCode || proof.CodeVerifier != testVerifier {
			t.Fatal("poll proof body did not match")
		}
		mu.Lock()
		response := responses[requestCount]
		requestCount++
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Retry-After", strconv.Itoa(response.retryAfter))
		writer.WriteHeader(response.status)
		_, _ = writer.Write([]byte(envelope(response.code, response.retryAfter)))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	client.wait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		if len(waits) == 3 {
			return context.Canceled
		}
		return nil
	}
	_, err = client.Poll(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey}, 4*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected test cancellation, got %v", err)
	}
	expected := []time.Duration{4 * time.Second, 4 * time.Second, 9 * time.Second}
	if len(waits) != len(expected) {
		t.Fatalf("unexpected waits: %v", waits)
	}
	for index := range expected {
		if waits[index] != expected[index] {
			t.Fatalf("wait %d = %s, expected %s", index, waits[index], expected[index])
		}
	}
}

func TestPollStopsOnTerminalStates(t *testing.T) {
	tests := []struct {
		status  int
		code    string
		outcome Outcome
	}{
		{http.StatusForbidden, codeDenied, OutcomeCancelled},
		{http.StatusGone, codeExpired, OutcomeExpired},
		{http.StatusConflict, codeConsumed, OutcomeConsumed},
		{http.StatusBadRequest, codeInvalidGrant, OutcomeInvalid},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(envelope(test.code, 0)))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
			if err != nil {
				t.Fatal(err)
			}
			client.wait = func(context.Context, time.Duration) error { return nil }
			result, err := client.Poll(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey}, 4*time.Second)
			if err != nil || result.Outcome != test.outcome {
				t.Fatalf("result = %#v, %v", result, err)
			}
		})
	}
}

func TestPollReturnsOnlyValidatedDeviceLimit(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "7")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(deviceLimitEnvelope("manage_devices", "device_limit_reached", true, 7, server.URL+"/settings/devices")))
	}))
	defer server.Close()
	store := newMemoryCredentialStore()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error { return nil }
	result, err := client.Poll(context.Background(), Credentials{
		DeviceCode:   testDeviceCode,
		CodeVerifier: testVerifier,
		DeviceKey:    testDeviceKey,
	}, minimumPollInterval)
	if err != nil || result.Outcome != OutcomeDeviceLimit || result.ErrorCode != codeDeviceLimit {
		t.Fatalf("device-limit result = %#v, %v", result, err)
	}
	if result.ManagementURL != server.URL+"/settings/devices" || result.RetryAfter != 7*time.Second {
		t.Fatalf("device-limit metadata = %#v", result)
	}
	if len(store.values) != 0 {
		t.Fatal("device-limit response wrote a credential")
	}
}

func TestPollRejectsUnsafeDeviceLimitMetadata(t *testing.T) {
	tests := []struct {
		name          string
		action        string
		state         string
		retryable     bool
		retryAfter    int
		managementURL func(string) string
	}{
		{name: "wrong action", action: "continue_polling", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/devices" }},
		{name: "wrong state", action: "manage_devices", state: "authorization_pending", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/devices" }},
		{name: "not retryable", action: "manage_devices", state: "device_limit_reached", retryable: false, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/devices" }},
		{name: "missing retry", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 0, managementURL: func(origin string) string { return origin + "/settings/devices" }},
		{name: "foreign origin", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(string) string { return "https://example.com/settings/devices" }},
		{name: "wrong scheme", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string {
			return strings.Replace(origin, "http://", "https://", 1) + "/settings/devices"
		}},
		{name: "wrong port", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(string) string { return "http://127.0.0.1:1/settings/devices" }},
		{name: "userinfo", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return strings.Replace(origin, "://", "://user@", 1) + "/settings/devices" }},
		{name: "query", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/devices?next=unsafe" }},
		{name: "fragment", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/devices#unsafe" }},
		{name: "encoded path", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/%73ettings/devices" }},
		{name: "wrong path", action: "manage_devices", state: "device_limit_reached", retryable: true, retryAfter: 7, managementURL: func(origin string) string { return origin + "/settings/billing" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(deviceLimitEnvelope(test.action, test.state, test.retryable, test.retryAfter, test.managementURL(server.URL))))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
			if err != nil {
				t.Fatal(err)
			}
			client.wait = func(context.Context, time.Duration) error { return nil }
			_, err = client.Poll(context.Background(), Credentials{
				DeviceCode:   testDeviceCode,
				CodeVerifier: testVerifier,
				DeviceKey:    testDeviceKey,
			}, minimumPollInterval)
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("unsafe device-limit metadata error = %v", err)
			}
		})
	}
}

func TestAuthorizeAndContinueRunsOriginalOperationAfterApproval(t *testing.T) {
	accessToken := syntheticAccessToken("R")
	refreshToken := strings.Repeat("S", 43)
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requestCount++
		if requestCount == 1 {
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(envelope(codePending, 4)))
			return
		}
		_, _ = writer.Write([]byte(tokenEnvelope(accessToken, refreshToken, testDeviceID)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error { return nil }
	originalInstruction := "install requested theme"
	operationCount := 0
	result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
		Credentials: Credentials{
			DeviceCode:   testDeviceCode,
			CodeVerifier: testVerifier,
			DeviceKey:    testDeviceKey,
		},
		InitialInterval: minimumPollInterval,
		Run: func(_ context.Context, authorized Result) error {
			operationCount++
			if originalInstruction != "install requested theme" || authorized.AccessToken == nil || authorized.AccessToken.Value() != accessToken {
				return errors.New("original operation context was not preserved")
			}
			return nil
		},
	})
	if err != nil || result.Outcome != OutcomeAuthorized || operationCount != 1 || requestCount != 2 {
		t.Fatalf("continuation result = %#v, requests = %d, operations = %d, error = %v", result, requestCount, operationCount, err)
	}
}

func TestAuthorizeAndContinueResumesSameProofAfterDeviceRevoke(t *testing.T) {
	accessToken := syntheticAccessToken("T")
	refreshToken := strings.Repeat("U", 43)
	store := newMemoryCredentialStore()
	var server *httptest.Server
	requestCount := 0
	deviceSlotReleased := false
	proofs := make([]proofRequest, 0, 2)
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var proof proofRequest
		if err := json.NewDecoder(request.Body).Decode(&proof); err != nil {
			t.Fatal(err)
		}
		proofs = append(proofs, proof)
		requestCount++
		if requestCount == 1 {
			writer.Header().Set("Retry-After", "7")
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(deviceLimitEnvelope("manage_devices", "device_limit_reached", true, 7, server.URL+"/settings/devices")))
			return
		}
		if !deviceSlotReleased {
			t.Fatal("authorization resumed before the device-slot callback completed")
		}
		_, _ = writer.Write([]byte(tokenEnvelope(accessToken, refreshToken, testDeviceID)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	client.wait = func(_ context.Context, duration time.Duration) error {
		waits = append(waits, duration)
		return nil
	}
	credentials := Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey}
	originalInstruction := "install requested theme"
	limitCount := 0
	operationCount := 0
	result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
		Credentials:     credentials,
		InitialInterval: minimumPollInterval,
		AwaitDeviceSlot: func(_ context.Context, limit DeviceLimit) error {
			limitCount++
			if limit.ManagementURL != server.URL+"/settings/devices" || limit.RetryAfter != 7*time.Second {
				return errors.New("device management metadata changed")
			}
			if len(store.values) != 0 {
				return errors.New("device-limit response persisted a credential")
			}
			deviceSlotReleased = true
			return nil
		},
		Run: func(_ context.Context, authorized Result) error {
			operationCount++
			if originalInstruction != "install requested theme" || authorized.AccessToken == nil || authorized.AccessToken.Value() != accessToken {
				return errors.New("original operation context was not preserved")
			}
			return nil
		},
	})
	if err != nil || result.Outcome != OutcomeAuthorized || limitCount != 1 || operationCount != 1 || requestCount != 2 {
		t.Fatalf("continuation result = %#v, limits = %d, operations = %d, requests = %d, error = %v", result, limitCount, operationCount, requestCount, err)
	}
	if len(waits) != 2 || waits[0] != minimumPollInterval || waits[1] != 7*time.Second {
		t.Fatalf("continuation waits = %v", waits)
	}
	for _, proof := range proofs {
		if proof.DeviceCode != credentials.DeviceCode || proof.CodeVerifier != credentials.CodeVerifier {
			t.Fatalf("continuation changed authorization proof: %#v", proofs)
		}
	}
	credential := store.credential(t, testDeviceID)
	if credential.RefreshToken != refreshToken || credential.DeviceKey != testDeviceKey {
		t.Fatal("continuation did not persist the successful token response")
	}
	formatted := fmt.Sprintf("%#v", Continuation{Credentials: credentials, InitialInterval: minimumPollInterval})
	if strings.Contains(formatted, testDeviceCode) || strings.Contains(formatted, testVerifier) || strings.Contains(formatted, testDeviceKey) {
		t.Fatal("formatted continuation exposed authorization credentials")
	}
}

func TestAuthorizeAndContinueDoesNotRunAfterBlockedOrTerminalResult(t *testing.T) {
	t.Run("management callback failure", func(t *testing.T) {
		callbackError := errors.New("device management was not completed")
		operationCount := 0
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(deviceLimitEnvelope("manage_devices", "device_limit_reached", true, 7, server.URL+"/settings/devices")))
		}))
		defer server.Close()
		client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
		if err != nil {
			t.Fatal(err)
		}
		client.wait = func(context.Context, time.Duration) error { return nil }
		result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
			Credentials:     Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey},
			InitialInterval: minimumPollInterval,
			AwaitDeviceSlot: func(context.Context, DeviceLimit) error { return callbackError },
			Run: func(context.Context, Result) error {
				operationCount++
				return nil
			},
		})
		if !errors.Is(err, callbackError) || result.Outcome != OutcomeDeviceLimit || operationCount != 0 {
			t.Fatalf("blocked continuation = %#v, operations = %d, error = %v", result, operationCount, err)
		}
	})

	t.Run("repeated limit returns without repeat prompt", func(t *testing.T) {
		requestCount := 0
		limitCount := 0
		operationCount := 0
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			requestCount++
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(deviceLimitEnvelope("manage_devices", "device_limit_reached", true, 4, server.URL+"/settings/devices")))
		}))
		defer server.Close()
		client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
		if err != nil {
			t.Fatal(err)
		}
		client.wait = func(context.Context, time.Duration) error { return nil }
		result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
			Credentials:     Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey},
			InitialInterval: minimumPollInterval,
			AwaitDeviceSlot: func(context.Context, DeviceLimit) error {
				limitCount++
				return nil
			},
			Run: func(context.Context, Result) error {
				operationCount++
				return nil
			},
		})
		if err != nil || result.Outcome != OutcomeDeviceLimit || requestCount != 2 || limitCount != 1 || operationCount != 0 {
			t.Fatalf("repeated limit = %#v, requests = %d, limits = %d, operations = %d, error = %v", result, requestCount, limitCount, operationCount, err)
		}
	})

	t.Run("terminal authorization", func(t *testing.T) {
		operationCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(envelope(codeDenied, 0)))
		}))
		defer server.Close()
		client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
		if err != nil {
			t.Fatal(err)
		}
		client.wait = func(context.Context, time.Duration) error { return nil }
		result, err := client.AuthorizeAndContinue(context.Background(), Continuation{
			Credentials:     Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier, DeviceKey: testDeviceKey},
			InitialInterval: minimumPollInterval,
			Run: func(context.Context, Result) error {
				operationCount++
				return nil
			},
		})
		if err != nil || result.Outcome != OutcomeCancelled || operationCount != 0 {
			t.Fatalf("terminal continuation = %#v, operations = %d, error = %v", result, operationCount, err)
		}
	})
}

func TestCancelAndErrorsDoNotExposeCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != cancelPath {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"status":"cancelled"},"requestId":"req_0123456789abcdef0123456789abcdef"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), newMemoryCredentialStore())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Cancel(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier})
	if err != nil || result.Outcome != OutcomeCancelled {
		t.Fatalf("cancel = %#v, %v", result, err)
	}

	broken, err := NewClient(server.URL, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(testDeviceCode + testVerifier)
	})}, newMemoryCredentialStore())
	if err != nil {
		t.Fatal(err)
	}
	_, requestErr := broken.Cancel(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier})
	if requestErr == nil || strings.Contains(requestErr.Error(), testDeviceCode) || strings.Contains(requestErr.Error(), testVerifier) {
		t.Fatalf("unsafe error: %v", requestErr)
	}
}

func TestGeneratedContractMatchesClientConstants(t *testing.T) {
	content, err := os.ReadFile("../../contracts/device-authorization-poll-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Endpoints map[string]string `json:"x-endpoints"`
		Defs      map[string]any    `json:"$defs"`
	}
	if err := json.Unmarshal(content, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Endpoints["poll"] != pollPath || schema.Endpoints["cancel"] != cancelPath || schema.Endpoints["refresh"] != refreshPath || schema.Endpoints["logout"] != "/api/v1/plugin/logout" {
		t.Fatalf("generated endpoint contract differs: %#v", schema.Endpoints)
	}
	encoded := string(content)
	for _, code := range []string{codeInvalidRequest, codeInvalidGrant, codePending, codeSlowDown, codeExpired, codeDenied, codeConsumed} {
		if !strings.Contains(encoded, `"`+code+`"`) {
			t.Fatalf("generated contract is missing %s", code)
		}
	}
	for _, code := range []string{"CS-AUTH-TOKEN-001", "CS-AUTH-TOKEN-002", "CS-AUTH-TOKEN-003", "CS-AUTH-TOKEN-004", "CS-AUTH-TOKEN-005", "CS-AUTH-TOKEN-006", "CS-AUTH-TOKEN-007"} {
		if !strings.Contains(encoded, `"`+code+`"`) {
			t.Fatalf("generated contract is missing %s", code)
		}
	}
	for _, code := range []string{"CS-AUTH-POLL-010", "CS-AUTH-LOGOUT-001", "CS-AUTH-LOGOUT-002", "CS-AUTH-LOGOUT-003", "CS-AUTH-LOGOUT-004"} {
		if !strings.Contains(encoded, `"`+code+`"`) {
			t.Fatalf("generated contract is missing %s", code)
		}
	}
	for _, definition := range []string{"proofRequest", "pollErrorEnvelope", "cancelSuccessEnvelope", "tokenSuccessEnvelope", "refreshRequest", "tokenErrorEnvelope", "logoutSuccessEnvelope", "logoutErrorEnvelope"} {
		if _, ok := schema.Defs[definition]; !ok {
			t.Fatalf("generated contract is missing %s", definition)
		}
	}
}

func TestPollStoresRefreshCredentialAndReturnsRedactedAccessToken(t *testing.T) {
	accessToken := syntheticAccessToken("D")
	refreshToken := strings.Repeat("E", 43)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != pollPath {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(tokenEnvelope(accessToken, refreshToken, testDeviceID)))
	}))
	defer server.Close()
	store := newMemoryCredentialStore()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error { return nil }
	result, err := client.Poll(context.Background(), Credentials{
		DeviceCode:   testDeviceCode,
		CodeVerifier: testVerifier,
		DeviceKey:    testDeviceKey,
	}, minimumPollInterval)
	if err != nil || result.Outcome != OutcomeAuthorized || result.AccessToken == nil {
		t.Fatalf("Poll did not authorize: %v", err)
	}
	if result.AccessToken.Value() != accessToken || result.AccessToken.ExpiresIn() != accessTokenLifetime {
		t.Fatal("Poll returned invalid Access Token metadata")
	}
	credential := store.credential(t, testDeviceID)
	if credential.RefreshToken != refreshToken || credential.DeviceKey != testDeviceKey {
		t.Fatal("Poll did not persist the rotated credential record")
	}
	formatted := fmt.Sprintf("%#v", result)
	if strings.Contains(formatted, accessToken) || strings.Contains(formatted, refreshToken) || strings.Contains(formatted, testDeviceKey) {
		t.Fatal("formatted Poll result exposed credential material")
	}
	assertSecretsAbsentFromProcessAndRepository(t, accessToken, refreshToken)
}

func TestRefreshRotatesCredentialOnlyAfterValidResponse(t *testing.T) {
	oldRefreshToken := strings.Repeat("F", 43)
	newRefreshToken := strings.Repeat("G", 43)
	newAccessToken := syntheticAccessToken("H")
	store := newMemoryCredentialStore()
	persistTestCredential(t, store, oldRefreshToken)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != refreshPath {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		var payload refreshRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.RefreshToken != oldRefreshToken || payload.DeviceKey != testDeviceKey {
			t.Fatal("refresh request did not use the stored credential")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(tokenEnvelope(newAccessToken, newRefreshToken, testDeviceID)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Refresh(context.Background(), testDeviceID)
	if err != nil || result.Outcome != OutcomeAuthorized || result.AccessToken == nil || result.AccessToken.Value() != newAccessToken {
		t.Fatalf("Refresh did not authorize: %v", err)
	}
	credential := store.credential(t, testDeviceID)
	if credential.RefreshToken != newRefreshToken || credential.DeviceKey != testDeviceKey {
		t.Fatal("Refresh did not replace the stored credential")
	}

	persistTestCredential(t, store, oldRefreshToken)
	invalidServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(tokenEnvelope(newAccessToken, newRefreshToken, "dev_abcdefghijklmnopqrstuvwxyz012345")))
	}))
	defer invalidServer.Close()
	invalidClient, err := NewClient(invalidServer.URL, invalidServer.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invalidClient.Refresh(context.Background(), testDeviceID); !errors.Is(err, ErrProtocol) {
		t.Fatalf("invalid refresh response error = %v", err)
	}
	if store.credential(t, testDeviceID).RefreshToken != oldRefreshToken {
		t.Fatal("invalid refresh response overwrote the existing credential")
	}
}

func TestRefreshSerializesConcurrentRotation(t *testing.T) {
	firstRefreshToken := strings.Repeat("M", 43)
	secondRefreshToken := strings.Repeat("N", 43)
	thirdRefreshToken := strings.Repeat("O", 43)
	store := newMemoryCredentialStore()
	persistTestCredential(t, store, firstRefreshToken)
	var requestMu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload refreshRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requestMu.Lock()
		defer requestMu.Unlock()
		expected := firstRefreshToken
		next := secondRefreshToken
		accessCharacter := "P"
		if requestCount == 1 {
			expected = secondRefreshToken
			next = thirdRefreshToken
			accessCharacter = "Q"
		}
		if requestCount > 1 || payload.RefreshToken != expected {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		requestCount++
		_, _ = writer.Write([]byte(tokenEnvelope(syntheticAccessToken(accessCharacter), next, testDeviceID)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			result, err := client.Refresh(context.Background(), testDeviceID)
			if err == nil && result.Outcome != OutcomeAuthorized {
				err = errors.New("concurrent Refresh did not authorize")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if requestCount != 2 || store.credential(t, testDeviceID).RefreshToken != thirdRefreshToken {
		t.Fatal("concurrent Refresh calls did not rotate in sequence")
	}
}

func TestRefreshDeletesTerminalCredentialAndKeepsRetryableCredential(t *testing.T) {
	for _, code := range []string{codeTokenInvalidGrant, codeTokenExpired, codeTokenReplay, codeTokenRevoked} {
		t.Run(code, func(t *testing.T) {
			store := newMemoryCredentialStore()
			persistTestCredential(t, store, strings.Repeat("I", 43))
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(envelope(code, 0)))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client(), store)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Refresh(context.Background(), testDeviceID)
			if err != nil || result.Outcome != OutcomeReauthorize || result.ErrorCode != code {
				t.Fatalf("terminal Refresh result = %v, %v", result.Outcome, err)
			}
			if _, err := store.Get(context.Background(), testDeviceID); err == nil {
				t.Fatal("terminal Refresh kept the revoked credential")
			}
		})
	}

	store := newMemoryCredentialStore()
	persistTestCredential(t, store, strings.Repeat("J", 43))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "17")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(envelope(codeTokenUnavailable, 17)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Refresh(context.Background(), testDeviceID)
	if err != nil || result.Outcome != OutcomeRetry || result.RetryAfter != 17*time.Second {
		t.Fatalf("retryable Refresh result = %v, %v", result.Outcome, err)
	}
	if store.credential(t, testDeviceID).RefreshToken != strings.Repeat("J", 43) {
		t.Fatal("retryable Refresh removed or changed the credential")
	}
}

func TestCredentialStoreFailuresDoNotExposeSecrets(t *testing.T) {
	refreshToken := strings.Repeat("K", 43)
	accessToken := syntheticAccessToken("L")
	store := newMemoryCredentialStore()
	store.putErr = errors.New(refreshToken + testDeviceKey)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(tokenEnvelope(accessToken, refreshToken, testDeviceID)))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), store)
	if err != nil {
		t.Fatal(err)
	}
	client.wait = func(context.Context, time.Duration) error { return nil }
	_, requestErr := client.Poll(context.Background(), Credentials{
		DeviceCode:   testDeviceCode,
		CodeVerifier: testVerifier,
		DeviceKey:    testDeviceKey,
	}, minimumPollInterval)
	if !errors.Is(requestErr, ErrCredentialStore) || strings.Contains(requestErr.Error(), refreshToken) || strings.Contains(requestErr.Error(), testDeviceKey) {
		t.Fatalf("unsafe credential store error: %v", requestErr)
	}
}

func syntheticAccessToken(character string) string {
	return strings.Repeat(character, 30) + "." + strings.Repeat(character, 30) + "." + strings.Repeat(character, 30)
}

func tokenEnvelope(accessToken, refreshToken, deviceID string) string {
	payload := map[string]any{
		"data": map[string]any{
			"tokenType":    "Bearer",
			"accessToken":  accessToken,
			"expiresIn":    900,
			"refreshToken": refreshToken,
			"device": map[string]any{
				"id":          deviceID,
				"displayName": "Synthetic device",
			},
		},
		"requestId": "req_0123456789abcdef0123456789abcdef",
	}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func persistTestCredential(t *testing.T, store *memoryCredentialStore, refreshToken string) {
	t.Helper()
	payload, err := json.Marshal(storedCredential{
		SchemaVersion: credentialSchema,
		RefreshToken:  refreshToken,
		DeviceKey:     testDeviceKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer zeroBytes(payload)
	if err := store.Put(context.Background(), testDeviceID, payload); err != nil {
		t.Fatal(err)
	}
}

func assertSecretsAbsentFromProcessAndRepository(t *testing.T, secrets ...string) {
	t.Helper()
	processMetadata := strings.Join(os.Args, "\x00") + "\x00" + strings.Join(os.Environ(), "\x00")
	for _, secret := range secrets {
		if strings.Contains(processMetadata, secret) {
			t.Fatal("credential appeared in process arguments or environment")
		}
	}
	root := filepath.Clean(filepath.Join("..", ".."))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "dist") {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 5*1024*1024 {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range secrets {
			if strings.Contains(string(contents), secret) {
				return errors.New("credential appeared in an ordinary repository file")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
