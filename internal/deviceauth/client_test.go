package deviceauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testDeviceCode = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	testVerifier   = "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

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

	client, err := NewClient(server.URL, server.Client())
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
	_, err = client.Poll(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier}, 4*time.Second)
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
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			client.wait = func(context.Context, time.Duration) error { return nil }
			result, err := client.Poll(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier}, 4*time.Second)
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
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Cancel(context.Background(), Credentials{DeviceCode: testDeviceCode, CodeVerifier: testVerifier})
	if err != nil || result.Outcome != OutcomeCancelled {
		t.Fatalf("cancel = %#v, %v", result, err)
	}

	broken, err := NewClient(server.URL, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New(testDeviceCode + testVerifier)
	})})
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
	if schema.Endpoints["poll"] != pollPath || schema.Endpoints["cancel"] != cancelPath || schema.Endpoints["refresh"] != "/api/v1/plugin/token/refresh" {
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
