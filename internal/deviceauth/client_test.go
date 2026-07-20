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
	if schema.Endpoints["poll"] != pollPath || schema.Endpoints["cancel"] != cancelPath || schema.Endpoints["refresh"] != refreshPath {
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
	for _, definition := range []string{"proofRequest", "pollErrorEnvelope", "cancelSuccessEnvelope", "tokenSuccessEnvelope", "refreshRequest", "tokenErrorEnvelope"} {
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
