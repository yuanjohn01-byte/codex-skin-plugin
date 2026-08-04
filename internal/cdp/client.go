// Package cdp provides a loopback-only Chromium DevTools Protocol client.
package cdp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxDiscoveryBytes = 256 * 1024
	defaultTimeout    = 5 * time.Second
)

var (
	ErrUnsafeEndpoint = errors.New("CDP endpoint is not a permitted loopback endpoint")
	ErrTargetInvalid  = errors.New("CDP page target is invalid")
	ErrProtocol       = errors.New("CDP protocol call failed")
	targetIDPattern   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,200}$`)
)

type Target struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	Title                string `json:"title"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type Client struct {
	connection *websocket.Conn
	mu         sync.Mutex
	nextID     int64
}

type protocolResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func Discover(ctx context.Context, port int) ([]Target, error) {
	if port < 1 || port > 65535 {
		return nil, ErrUnsafeEndpoint
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port) + "/json/list"
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" {
				return nil, ErrUnsafeEndpoint
			}
			if address != "127.0.0.1:"+strconv.Itoa(port) {
				return nil, ErrUnsafeEndpoint
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp4", address)
		},
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   defaultTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: discovery request", ErrProtocol)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "application/json") {
		return nil, fmt.Errorf("%w: discovery response", ErrProtocol)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxDiscoveryBytes+1))
	if err != nil || len(content) < 2 || len(content) > maxDiscoveryBytes {
		return nil, fmt.Errorf("%w: discovery body", ErrProtocol)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	var rawTargets []Target
	if err := decoder.Decode(&rawTargets); err != nil {
		return nil, fmt.Errorf("%w: discovery JSON", ErrProtocol)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: discovery trailing data", ErrProtocol)
	}
	targets := make([]Target, 0, len(rawTargets))
	for _, target := range rawTargets {
		if validateTarget(target, port) == nil {
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, ErrTargetInvalid
	}
	return targets, nil
}

func SelectPage(targets []Target) (Target, error) {
	mainTargets := make([]Target, 0, len(targets))
	for _, target := range targets {
		if target.URL == "app://-/index.html" ||
			target.URL == "app://codex/index.html" {
			mainTargets = append(mainTargets, target)
		}
	}
	if len(mainTargets) != 1 {
		return Target{}, fmt.Errorf(
			"%w: expected one official main app page, got %d",
			ErrTargetInvalid,
			len(mainTargets),
		)
	}
	return mainTargets[0], nil
}

func Dial(ctx context.Context, target Target, port int) (*Client, error) {
	if err := validateTarget(target, port); err != nil {
		return nil, err
	}
	dialer := websocket.Dialer{
		Proxy: nil,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" && network != "tcp4" {
				return nil, ErrUnsafeEndpoint
			}
			if address != "127.0.0.1:"+strconv.Itoa(port) {
				return nil, ErrUnsafeEndpoint
			}
			var netDialer net.Dialer
			return netDialer.DialContext(ctx, "tcp4", address)
		},
		HandshakeTimeout: defaultTimeout,
	}
	connection, response, err := dialer.DialContext(ctx, target.WebSocketDebuggerURL, nil)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: websocket dial", ErrProtocol)
	}
	return &Client{connection: connection}, nil
}

func (client *Client) Call(ctx context.Context, method string, params any, result any) error {
	if client == nil || client.connection == nil || method == "" || len(method) > 128 {
		return ErrProtocol
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	client.nextID++
	id := client.nextID
	deadline := time.Now().Add(defaultTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := client.connection.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("%w: write deadline", ErrProtocol)
	}
	if err := client.connection.WriteJSON(struct {
		ID     int64  `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{ID: id, Method: method, Params: params}); err != nil {
		return fmt.Errorf("%w: write request", ErrProtocol)
	}
	if err := client.connection.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("%w: read deadline", ErrProtocol)
	}
	for messages := 0; messages < 256; messages++ {
		_, raw, err := client.connection.ReadMessage()
		if err != nil {
			return fmt.Errorf("%w: read response", ErrProtocol)
		}
		var response protocolResponse
		if err := json.Unmarshal(raw, &response); err != nil {
			return fmt.Errorf("%w: response JSON", ErrProtocol)
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%w: method %s code %d", ErrProtocol, method, response.Error.Code)
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 || json.Unmarshal(response.Result, result) != nil {
			return fmt.Errorf("%w: result shape", ErrProtocol)
		}
		return nil
	}
	return fmt.Errorf("%w: response event limit", ErrProtocol)
}

func (client *Client) Close() error {
	if client == nil || client.connection == nil {
		return nil
	}
	return client.connection.Close()
}

func validateTarget(target Target, port int) error {
	if port < 1 ||
		port > 65535 ||
		target.Type != "page" ||
		!targetIDPattern.MatchString(target.ID) ||
		!strings.HasPrefix(target.URL, "app://") {
		return ErrTargetInvalid
	}
	parsed, err := url.Parse(target.WebSocketDebuggerURL)
	if err != nil ||
		parsed.Scheme != "ws" ||
		parsed.Hostname() != "127.0.0.1" ||
		parsed.Port() != strconv.Itoa(port) ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		parsed.Path != "/devtools/page/"+target.ID {
		return ErrUnsafeEndpoint
	}
	return nil
}
