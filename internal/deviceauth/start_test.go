package deviceauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

type startMemoryStore struct{}

func (startMemoryStore) Put(context.Context, string, []byte) error { return nil }
func (startMemoryStore) Get(context.Context, string) ([]byte, error) {
	return nil, errors.New("not found")
}
func (startMemoryStore) Delete(context.Context, string) error { return nil }

func TestStartCreatesPKCEAuthorization(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != startPath {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(request.Header.Get("Idempotency-Key")) {
			t.Errorf("invalid idempotency key")
		}
		var body startRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !validOpaqueSecret(body.DeviceKey) ||
			!validOpaqueSecret(body.CodeChallenge) ||
			body.DeviceDisplayName != "Founder Mac" ||
			body.Platform != "macos" ||
			body.PluginVersion != "0.1.0-paid-alpha" ||
			body.EngineVersion != "0.2.0" ||
			body.PluginProtocolVersion != 1 {
			t.Errorf("invalid start body: %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"data":{"deviceCode":"` + strings.Repeat("d", 43) + `","verificationUriComplete":"` + server.URL + `/device/approve?token=` + strings.Repeat("a", 43) + `","expiresIn":300,"interval":4},"requestId":"req_` + strings.Repeat("b", 32) + `"}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client(), startMemoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Start(context.Background(), StartInput{
		DeviceDisplayName: "Founder Mac",
		Platform:          "macos",
		PluginVersion:     "0.1.0-paid-alpha",
		EngineVersion:     "0.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeStarted ||
		result.Interval != minimumPollInterval ||
		result.ExpiresIn.String() != "5m0s" ||
		result.VerificationURL == "" ||
		!validProofCredentials(result.Credentials) ||
		!validOpaqueSecret(result.Credentials.DeviceKey) {
		t.Fatalf("unexpected start result: %+v", result)
	}
	challenge := sha256.Sum256([]byte(result.Credentials.CodeVerifier))
	if encoded := base64.RawURLEncoding.EncodeToString(challenge[:]); !validOpaqueSecret(encoded) {
		t.Fatalf("invalid generated PKCE challenge: %q", encoded)
	}
}

func TestStartRejectsCrossOriginVerificationAndMalformedReplay(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header string
		body   string
	}{
		{
			name:   "cross origin",
			status: http.StatusCreated,
			body:   `{"data":{"deviceCode":"` + strings.Repeat("d", 43) + `","verificationUriComplete":"https://evil.example/device/approve?token=` + strings.Repeat("a", 43) + `","expiresIn":300,"interval":4},"requestId":"req_` + strings.Repeat("b", 32) + `"}`,
		},
		{
			name:   "200 without replay marker",
			status: http.StatusOK,
			body:   `{"data":{"deviceCode":"` + strings.Repeat("d", 43) + `","verificationUriComplete":"PLACEHOLDER/device/approve?token=` + strings.Repeat("a", 43) + `","expiresIn":300,"interval":4},"requestId":"req_` + strings.Repeat("b", 32) + `"}`,
		},
		{
			name:   "201 with replay marker",
			status: http.StatusCreated,
			header: "true",
			body:   `{"data":{"deviceCode":"` + strings.Repeat("d", 43) + `","verificationUriComplete":"PLACEHOLDER/device/approve?token=` + strings.Repeat("a", 43) + `","expiresIn":300,"interval":4},"requestId":"req_` + strings.Repeat("b", 32) + `"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.header != "" {
					writer.Header().Set("Idempotency-Replayed", test.header)
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(strings.ReplaceAll(test.body, "PLACEHOLDER", server.URL)))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client(), startMemoryStore{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Start(context.Background(), StartInput{
				DeviceDisplayName: "Founder Mac",
				Platform:          "macos",
				PluginVersion:     "0.1.0-paid-alpha",
				EngineVersion:     "0.2.0",
			})
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("Start error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestStartReturnsRetryableServerOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "9")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"code":"CS-AUTH-START-004","message":"limited","action":"retry_later","retryable":true,"incidentId":null,"details":{"retryAfter":7}},"requestId":"req_` + strings.Repeat("c", 32) + `"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), startMemoryStore{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Start(context.Background(), StartInput{
		DeviceDisplayName: "Founder Mac",
		Platform:          "macos",
		PluginVersion:     "0.1.0-paid-alpha",
		EngineVersion:     "0.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeRetry || result.RetryAfter.String() != "9s" {
		t.Fatalf("unexpected retry result: %+v", result)
	}
}
