package adapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/cdp"
	"github.com/yuanjohn01-byte/codex-skin-plugin/internal/engine"
)

func TestInstallControllerRegistersPersistentBootstrapAndReplacesIt(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var mutex sync.Mutex
	methods := []string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/page/page-1", func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			var call map[string]any
			if err := connection.ReadJSON(&call); err != nil {
				return
			}
			method, _ := call["method"].(string)
			mutex.Lock()
			methods = append(methods, method)
			mutex.Unlock()
			result := map[string]any{}
			switch method {
			case "Page.addScriptToEvaluateOnNewDocument":
				result["identifier"] = "bootstrap-1"
			case "Runtime.evaluate":
				result["result"] = map[string]any{"type": "object", "objectId": "global-1"}
			case "Runtime.callFunctionOn":
				result["result"] = map[string]any{"type": "boolean", "value": true}
			}
			if err := connection.WriteJSON(map[string]any{"id": call["id"], "result": result}); err != nil {
				return
			}
		}
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
	target := cdp.Target{
		ID: "page-1", Type: "page", URL: "app://codex/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:" + parsed.Port() + "/devtools/page/page-1",
	}
	client, err := cdp.Dial(context.Background(), target, port)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := &Live{root: root}
	live := &liveSession{client: client, targetID: target.ID}
	compiled := engine.CompiledTheme{
		ThemePublicID: "100002", ThemeVersion: "1.0.0",
		TemplateVersion: engine.TemplateVersion, AppearanceMode: "dark",
		StyleText:         "body { color: white; }",
		BackgroundDataURL: "data:image/png;base64,AA==",
	}
	if err := adapter.installController(context.Background(), live, compiled); err != nil {
		t.Fatal(err)
	}
	record, found, err := adapter.readControllerRecord()
	if err != nil || !found || record.TargetID != "page-1" || record.Identifier != "bootstrap-1" {
		t.Fatalf("controller record = %#v, found=%v, err=%v", record, found, err)
	}
	if err := adapter.installController(context.Background(), live, compiled); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	want := []string{
		"Page.addScriptToEvaluateOnNewDocument",
		"Runtime.evaluate",
		"Runtime.callFunctionOn",
		"Page.removeScriptToEvaluateOnNewDocument",
		"Page.addScriptToEvaluateOnNewDocument",
		"Runtime.evaluate",
		"Runtime.callFunctionOn",
	}
	if len(methods) != len(want) {
		t.Fatalf("CDP methods = %v, want %v", methods, want)
	}
	for index := range want {
		if methods[index] != want[index] {
			t.Fatalf("CDP methods = %v, want %v", methods, want)
		}
	}
}

func TestRemoveControllerReplacesStaleCDPIdentifierWithNeutralizer(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	var mutex sync.Mutex
	methods := []string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/devtools/page/page-1", func(writer http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			var call map[string]any
			if err := connection.ReadJSON(&call); err != nil {
				return
			}
			method, _ := call["method"].(string)
			mutex.Lock()
			methods = append(methods, method)
			mutex.Unlock()
			if method == "Page.removeScriptToEvaluateOnNewDocument" {
				if err := connection.WriteJSON(map[string]any{
					"id":    call["id"],
					"error": map[string]any{"code": -32000, "message": "No script with given id"},
				}); err != nil {
					return
				}
				continue
			}
			result := map[string]any{}
			switch method {
			case "Page.addScriptToEvaluateOnNewDocument":
				result["identifier"] = "neutralizer-1"
			case "Runtime.evaluate":
				result["result"] = map[string]any{"type": "object", "objectId": "global-1"}
			case "Runtime.callFunctionOn":
				result["result"] = map[string]any{"type": "boolean", "value": true}
			}
			if err := connection.WriteJSON(map[string]any{"id": call["id"], "result": result}); err != nil {
				return
			}
		}
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
	target := cdp.Target{
		ID: "page-1", Type: "page", URL: "app://codex/index.html",
		WebSocketDebuggerURL: "ws://127.0.0.1:" + parsed.Port() + "/devtools/page/page-1",
	}
	client, err := cdp.Dial(context.Background(), target, port)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	adapter := &Live{root: root}
	if err := adapter.writeControllerRecord(controllerRecord{
		SchemaVersion: 1, TargetID: target.ID, Identifier: "stale-bootstrap",
	}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.removeControllerBootstrap(
		context.Background(),
		&liveSession{client: client, targetID: target.ID},
	); err != nil {
		t.Fatal(err)
	}
	if _, found, err := adapter.readControllerRecord(); err != nil || found {
		t.Fatalf("controller record remains: found=%t err=%v", found, err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	want := []string{
		"Page.removeScriptToEvaluateOnNewDocument",
		"Page.addScriptToEvaluateOnNewDocument",
		"Runtime.evaluate",
		"Runtime.callFunctionOn",
	}
	if len(methods) != len(want) {
		t.Fatalf("CDP methods = %v, want %v", methods, want)
	}
	for index := range want {
		if methods[index] != want[index] {
			t.Fatalf("CDP methods = %v, want %v", methods, want)
		}
	}
}
