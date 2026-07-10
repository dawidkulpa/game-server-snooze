package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckHealthAcceptsOnlySuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s", request.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := checkHealth(ctx, server.URL); err != nil {
		t.Fatalf("checkHealth() error = %v", err)
	}
}

func TestCheckHealthRejectsNonSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := checkHealth(ctx, server.URL); err == nil {
		t.Fatal("checkHealth() accepted a non-successful status")
	}
}

func TestCheckHealthRejectsNonLoopbackEndpoint(t *testing.T) {
	if err := checkHealth(context.Background(), "https://example.com/healthz"); err == nil {
		t.Fatal("expected non-loopback endpoint to be rejected")
	}
}

func TestCheckHealthRejectsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		http.Redirect(w, request, "http://example.com/", http.StatusFound)
	}))
	defer server.Close()
	if err := checkHealth(context.Background(), server.URL); err == nil {
		t.Fatal("expected health redirect to be rejected")
	}
}

func TestCheckHealthHonorsContextDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := checkHealth(ctx, server.URL); err == nil {
		t.Fatal("expected health request to honor context deadline")
	}
}
