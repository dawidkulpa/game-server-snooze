package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dkulpa.eu/game-server-snooze/pkg/config"
)

func TestPterodactylClientImplementsStatusAndPowerContract(t *testing.T) {
	var mu sync.Mutex
	var powers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected authentication headers")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/client/servers/server-id/resources":
			_, _ = io.WriteString(w, `{"object":"stats","attributes":{"current_state":"running"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/client/servers/server-id/power":
			var body struct {
				Signal string `json:"signal"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode power body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			powers = append(powers, body.Signal)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  server.URL,
		APIToken: "test-token",
		ServerID: "server-id",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state, err := client.Status(context.Background())
	if err != nil || state != StateRunning {
		t.Fatalf("Status() = state %q error %v", state, err)
	}
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(powers) != 2 || powers[0] != "start" || powers[1] != "stop" {
		t.Fatalf("power signals = %v", powers)
	}
}

func TestPterodactylClientRejectsSensitiveBaseURLMaterialAndUnsafeServerID(t *testing.T) {
	for name, cfg := range map[string]config.PterodactylConfig{
		"query":          {BaseURL: "https://panel.example.test?access_token=secret", APIToken: "token", ServerID: "server-id"},
		"fragment":       {BaseURL: "https://panel.example.test/#secret", APIToken: "token", ServerID: "server-id"},
		"server ID path": {BaseURL: "https://panel.example.test", APIToken: "token", ServerID: "private/server id"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewPterodactylClient(cfg, nil); err == nil {
				t.Fatal("accepted unsafe Pterodactyl request material")
			}
		})
	}
}

func TestPterodactylClientRejectsUnknownServerState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"attributes":{"current_state":"mystery"}}`)
	}))
	defer server.Close()
	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  server.URL,
		APIToken: "test-token",
		ServerID: "server-id",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := client.Status(context.Background()); err == nil {
		t.Fatal("Status() accepted an unknown Pterodactyl state")
	}
}

func TestPterodactylErrorsBoundAndRedactResponseBody(t *testing.T) {
	const token = "super-secret-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, "panel-error "+token+" server-id "+strings.Repeat("x", 10000))
	}))
	defer server.Close()
	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  server.URL,
		APIToken: token,
		ServerID: "server-id",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Status(context.Background())
	if err == nil {
		t.Fatal("Status() accepted HTTP 403")
	}
	message := err.Error()
	if strings.Contains(message, token) || strings.Contains(message, "server-id") {
		t.Fatal("Status() error exposed private request material")
	}
	if !strings.Contains(message, "panel-error") || !strings.Contains(message, "[REDACTED]") || !strings.Contains(message, "truncated") {
		t.Fatalf("Status() error lacks bounded sanitized context: %q", message)
	}
	if len(message) > 5000 {
		t.Fatalf("Status() error is unbounded: %d bytes", len(message))
	}
	if !IsPermanent(err) {
		t.Fatal("HTTP 403 status error was not classified as permanent")
	}
}

func TestPterodactylTransportErrorsRedactServerIDAndPreserveCancellation(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	baseURL := server.URL
	server.Close()
	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  baseURL,
		APIToken: "test-token",
		ServerID: "private-server-id",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Status(context.Background()); err == nil || strings.Contains(err.Error(), "private-server-id") {
		t.Fatalf("transport error was missing or exposed server ID: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Status(ctx)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "private-server-id") {
		t.Fatalf("cancellation was not preserved and redacted: %v", err)
	}
}

func TestPterodactylStatusBodyIsBoundedAndSingleDocument(t *testing.T) {
	cases := map[string]string{
		"oversized": strings.Repeat("x", maxPterodactylStatusBody+1),
		"trailing":  `{"attributes":{"current_state":"running"}} {}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client, err := NewPterodactylClient(config.PterodactylConfig{
				BaseURL: server.URL, APIToken: "test-token", ServerID: "server-id",
			}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Status(context.Background())
			if err == nil || !IsPermanent(err) || len(err.Error()) > 512 {
				t.Fatalf("status body was not rejected safely: %v", err)
			}
		})
	}
}

func TestPterodactylClientRejectsRedirectsWithoutForwardingAuthorization(t *testing.T) {
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		targetCalls++
		if request.Header.Get("Authorization") != "" {
			t.Error("redirect target received authorization")
		}
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, target.URL, http.StatusFound)
	}))
	defer redirector.Close()
	injected := &http.Client{}
	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL: redirector.URL, APIToken: "test-token", ServerID: "server-id",
	}, injected)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Status(context.Background()); err == nil {
		t.Fatal("redirect response was accepted")
	}
	if targetCalls != 0 {
		t.Fatalf("redirect target calls = %d, want 0", targetCalls)
	}
	if client.httpClient.Timeout != 10*time.Second || injected.Timeout != 0 {
		t.Fatalf("safe timeout not enforced on clone: safe=%s injected=%s", client.httpClient.Timeout, injected.Timeout)
	}
}

func TestPterodactylClientRejectsRemotePlaintextHTTP(t *testing.T) {
	_, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  "http://panel.example.com",
		APIToken: "test-token",
		ServerID: "server-id",
	}, nil)
	if err == nil {
		t.Fatal("constructor accepted a remote plaintext endpoint")
	}
}

func TestPterodactylRateLimitErrorIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client, err := NewPterodactylClient(config.PterodactylConfig{
		BaseURL:  server.URL,
		APIToken: "test-token",
		ServerID: "server-id",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Status(context.Background())
	if err == nil || IsPermanent(err) {
		t.Fatalf("HTTP 429 classification error: %v", err)
	}
}
