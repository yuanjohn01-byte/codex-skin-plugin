package themeapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/theme"
)

const testBearer = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ0ZXN0LXVzZXItZm9yLXRoZW1lLWFwaSJ9.c2lnbmF0dXJlLXdpdGgtZW5vdWdoLWJhc2U2NHVybC1ieXRlcw"

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "free-test-theme-v1", "signed-release-v1", name))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func successPayload(t *testing.T, descriptorRaw, signatureRaw []byte) []byte {
	t.Helper()
	var descriptor theme.Descriptor
	if err := json.Unmarshal(descriptorRaw, &descriptor); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"data": map[string]any{
			"themePublicId":     descriptor.ThemePublicID,
			"themeVersion":      descriptor.ThemeVersion,
			"tier":              "free",
			"releaseDescriptor": descriptor,
			"signature":         strings.TrimSpace(string(signatureRaw)),
			"minEngineVersion":  "0.1.0",
			"downloadPath":      "/api/v1/plugin/themes/100001/download",
		},
		"requestId": "req_" + strings.Repeat("a", 32),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMetadataDownloadAndVerify(t *testing.T) {
	descriptorRaw := fixture(t, "release-descriptor.json")
	signatureRaw := fixture(t, "release-descriptor.sig")
	packageRaw := fixture(t, "package.cskin")
	metadataRaw := successPayload(t, descriptorRaw, signatureRaw)
	eventSeen := false

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+testBearer ||
			request.Header.Get("X-Codex-Skin-Protocol") != "1" {
			t.Errorf("missing authorization/protocol headers")
		}
		switch request.URL.Path {
		case "/api/v1/plugin/themes/100001":
			if request.Method != http.MethodGet {
				t.Errorf("metadata method = %s", request.Method)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(metadataRaw)
		case "/api/v1/plugin/themes/100001/download":
			if request.Method != http.MethodPost {
				t.Errorf("download method = %s", request.Method)
			}
			var body struct {
				ThemeVersion string `json:"themeVersion"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.ThemeVersion != "1.0.0" {
				t.Errorf("download body invalid: %v, %+v", err, body)
			}
			writer.Header().Set("Content-Type", "application/octet-stream")
			writer.Header().Set("Content-Length", "1933")
			_, _ = writer.Write(packageRaw)
		case "/api/v1/plugin/events":
			if request.Method != http.MethodPost {
				t.Errorf("event method = %s", request.Method)
			}
			var body struct {
				Events []struct {
					ID            string `json:"id"`
					EventName     string `json:"eventName"`
					OccurredAt    string `json:"occurredAt"`
					ThemePublicID string `json:"themePublicId"`
					ThemeVersion  string `json:"themeVersion"`
					Result        string `json:"result"`
					DurationMS    int64  `json:"durationMs"`
				} `json:"events"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil ||
				len(body.Events) != 1 ||
				!strings.HasPrefix(body.Events[0].ID, "evt_") ||
				body.Events[0].EventName != "theme_apply_succeeded" ||
				body.Events[0].OccurredAt != "2026-07-30T12:00:00.000Z" ||
				body.Events[0].ThemePublicID != "100001" ||
				body.Events[0].ThemeVersion != "1.0.0" ||
				body.Events[0].Result != "succeeded" ||
				body.Events[0].DurationMS != 1250 {
				t.Errorf("event body invalid: %v, %+v", err, body)
			}
			eventSeen = true
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"data":{"accepted":1},"requestId":"req_ffffffffffffffffffffffffffffffff"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Metadata(context.Background(), "100001", testBearer)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != OutcomeReady || result.Release == nil {
		t.Fatalf("unexpected metadata result: %+v", result)
	}
	destination := filepath.Join(t.TempDir(), "package.cskin.part")
	if err := client.Download(context.Background(), *result.Release, testBearer, destination); err != nil {
		t.Fatal(err)
	}
	verified, err := theme.Verify(destination, result.Release.DescriptorBytes, result.Release.SignatureBytes)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Manifest.ThemePublicID != "100001" || verified.Manifest.ThemeVersion != "1.0.0" {
		t.Fatalf("unexpected verified fixture: %+v", verified.Manifest)
	}
	if err := client.RecordApply(
		context.Background(),
		*result.Release,
		testBearer,
		time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		1250*time.Millisecond,
	); err != nil {
		t.Fatal(err)
	}
	if !eventSeen {
		t.Fatal("apply event was not recorded")
	}
}

func TestMetadataOutcomesAndFailClosedResponses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		outcome Outcome
	}{
		{
			name:    "reauthorize",
			status:  http.StatusUnauthorized,
			body:    `{"error":{"code":"CS-THEME-RELEASE-002","message":"authorization","action":"reauthorize","retryable":false,"incidentId":null,"details":{}},"requestId":"req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`,
			outcome: OutcomeReauthorize,
		},
		{
			name:    "purchase",
			status:  http.StatusForbidden,
			body:    `{"error":{"code":"CS-THEME-RELEASE-003","message":"purchase","action":"purchase_access","retryable":false,"incidentId":null,"details":{"themePublicId":"100001","themeVersion":"1.0.0","pricingPath":"/pricing?theme=100001"}},"requestId":"req_cccccccccccccccccccccccccccccccc"}`,
			outcome: OutcomeAccessRequired,
		},
		{
			name:    "unavailable",
			status:  http.StatusNotFound,
			body:    `{"error":{"code":"CS-THEME-RELEASE-004","message":"missing","action":"choose_available_theme","retryable":false,"incidentId":null,"details":{}},"requestId":"req_dddddddddddddddddddddddddddddddd"}`,
			outcome: OutcomeUnavailable,
		},
		{
			name:    "retry",
			status:  http.StatusInternalServerError,
			body:    `{"error":{"code":"CS-THEME-RELEASE-005","message":"retry","action":"retry_or_contact_support","retryable":true,"incidentId":"INC-ABCDEF012345","details":{}},"requestId":"req_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}`,
			outcome: OutcomeRetry,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Metadata(context.Background(), "100001", testBearer)
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != test.outcome {
				t.Fatalf("outcome = %q, want %q", result.Outcome, test.outcome)
			}
			if test.outcome == OutcomeAccessRequired && result.PricingPath != "/pricing?theme=100001" {
				t.Fatalf("pricing path = %q", result.PricingPath)
			}
		})
	}

	badBodies := []string{
		`{"data":{},"requestId":"req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","extra":true}`,
		`{"error":{"code":"CS-THEME-RELEASE-003","message":"purchase","action":"purchase_access","retryable":false,"incidentId":null,"details":{"themePublicId":"100001","themeVersion":"1.0.0","pricingPath":"https://evil.example"}},"requestId":"req_cccccccccccccccccccccccccccccccc"}`,
	}
	for _, body := range badBodies {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(body))
		}))
		client, err := NewClient(server.URL, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Metadata(context.Background(), "100001", testBearer); !errors.Is(err, ErrProtocol) {
			t.Fatalf("bad response error = %v, want ErrProtocol", err)
		}
		server.Close()
	}
}

func TestDownloadRejectsTruncationRedirectAndOversize(t *testing.T) {
	descriptorRaw := fixture(t, "release-descriptor.json")
	signatureRaw := fixture(t, "release-descriptor.sig")
	packageRaw := fixture(t, "package.cskin")
	var descriptor theme.Descriptor
	if err := json.Unmarshal(descriptorRaw, &descriptor); err != nil {
		t.Fatal(err)
	}
	release := Release{
		ThemePublicID:    "100001",
		ThemeVersion:     "1.0.0",
		Descriptor:       descriptor,
		DescriptorBytes:  descriptorRaw,
		SignatureBytes:   signatureRaw,
		MinEngineVersion: "0.1.0",
		DownloadPath:     "/api/v1/plugin/themes/100001/download",
	}
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "truncated",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/octet-stream")
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write(packageRaw[:100])
			},
		},
		{
			name: "redirect",
			handler: func(writer http.ResponseWriter, request *http.Request) {
				http.Redirect(writer, request, "https://evil.example/package", http.StatusFound)
			},
		},
		{
			name: "oversize",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/octet-stream")
				writer.WriteHeader(http.StatusOK)
				_, _ = writer.Write(append(packageRaw, 0))
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client, err := NewClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(t.TempDir(), "package.cskin.part")
			if err := client.Download(context.Background(), release, testBearer, destination); !errors.Is(err, ErrDownload) {
				t.Fatalf("download error = %v, want ErrDownload", err)
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("unsafe partial file remained: %v", err)
			}
		})
	}
}

func TestNewClientRejectsUnsafeBaseURLs(t *testing.T) {
	for _, value := range []string{
		"http://example.com",
		"https://user@example.com",
		"https://example.com/path",
		"file:///tmp/api",
	} {
		if _, err := NewClient(value, nil); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatalf("NewClient(%q) error = %v", value, err)
		}
	}
}
