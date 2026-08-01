package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/gorilla/websocket"
)

func TestDiscoverDialAndCallStayOnExactLoopback(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(writer http.ResponseWriter, request *http.Request) {
		parsed, _ := url.Parse(serverURL)
		port := parsed.Port()
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode([]Target{{
			ID: "page-1", Type: "page", URL: "app://codex/index.html", Title: "Codex",
			WebSocketDebuggerURL: "ws://127.0.0.1:" + port + "/devtools/page/page-1",
		}})
	})
	mux.HandleFunc("/devtools/page/page-1", func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var call map[string]any
		if err := connection.ReadJSON(&call); err != nil {
			return
		}
		_ = connection.WriteJSON(map[string]any{"method": "Runtime.consoleAPICalled", "params": map[string]any{}})
		_ = connection.WriteJSON(map[string]any{
			"id": call["id"], "result": map[string]any{"value": "ok"},
		})
	})
	server := httptest.NewUnstartedServer(mux)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	serverURL = server.URL
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())

	targets, err := Discover(context.Background(), port)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	target, err := SelectPage(targets)
	if err != nil {
		t.Fatal(err)
	}
	client, err := Dial(context.Background(), target, port)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer client.Close()
	var result struct {
		Value string `json:"value"`
	}
	if err := client.Call(context.Background(), "Runtime.evaluate", map[string]any{"expression": "1"}, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result.Value != "ok" {
		t.Fatalf("result = %#v", result)
	}
}

func TestTargetValidationRejectsNonLoopbackAndAmbiguity(t *testing.T) {
	base := Target{
		ID: "page-1", Type: "page", URL: "app://codex/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:9222/devtools/page/page-1",
	}
	cases := []Target{
		{ID: base.ID, Type: base.Type, URL: base.URL, WebSocketDebuggerURL: "ws://localhost:9222/devtools/page/page-1"},
		{ID: base.ID, Type: base.Type, URL: base.URL, WebSocketDebuggerURL: "ws://192.168.1.2:9222/devtools/page/page-1"},
		{ID: base.ID, Type: base.Type, URL: "https://example.com", WebSocketDebuggerURL: base.WebSocketDebuggerURL},
		{ID: "../page", Type: base.Type, URL: base.URL, WebSocketDebuggerURL: base.WebSocketDebuggerURL},
		{ID: base.ID, Type: base.Type, URL: base.URL, WebSocketDebuggerURL: base.WebSocketDebuggerURL + "?token=x"},
	}
	for _, target := range cases {
		if err := validateTarget(target, 9222); err == nil {
			t.Fatalf("validateTarget(%#v) succeeded", target)
		}
	}
	if _, err := SelectPage([]Target{base, base}); !errors.Is(err, ErrTargetInvalid) {
		t.Fatalf("SelectPage() error = %v", err)
	}
}

func TestSelectPageIgnoresOfficialOverlayAndRequiresOneMainSurface(t *testing.T) {
	main := Target{
		ID: "main-page", Type: "page", URL: "app://-/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:9222/devtools/page/main-page",
	}
	overlay := Target{
		ID: "avatar-overlay", Type: "page",
		URL:                  "app://-/index.html?initialRoute=%2Favatar-overlay",
		WebSocketDebuggerURL: "ws://127.0.0.1:9222/devtools/page/avatar-overlay",
	}
	got, err := SelectPage([]Target{overlay, main})
	if err != nil {
		t.Fatalf("SelectPage() error = %v", err)
	}
	if got != main {
		t.Fatalf("SelectPage() = %#v, want %#v", got, main)
	}
	if _, err := SelectPage([]Target{overlay}); !errors.Is(err, ErrTargetInvalid) {
		t.Fatalf("overlay-only SelectPage() error = %v", err)
	}
}

func TestCallReturnsProtocolErrorWithoutLeakingRemoteMessage(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/page/page-1", func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		var call map[string]any
		_ = connection.ReadJSON(&call)
		_ = connection.WriteJSON(map[string]any{
			"id": call["id"], "error": map[string]any{"code": -32000, "message": "sensitive renderer detail"},
		})
	})
	server := httptest.NewUnstartedServer(mux)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(parsed.Port())
	target := Target{
		ID: "page-1", Type: "page", URL: "app://codex/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:" + parsed.Port() + "/devtools/page/page-1",
	}
	client, err := Dial(context.Background(), target, port)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	err = client.Call(context.Background(), "Runtime.evaluate", nil, nil)
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("Call() error = %v", err)
	}
	if err != nil && contains(err.Error(), "sensitive renderer detail") {
		t.Fatalf("Call() leaked remote message: %v", err)
	}
}

func contains(value, part string) bool {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return true
		}
	}
	return false
}
