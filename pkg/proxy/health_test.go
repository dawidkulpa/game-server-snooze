package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerSeparatesLivenessFromReversibleReadiness(t *testing.T) {
	state := &HealthState{}
	handler := NewHealthHandler(state)

	liveness := httptest.NewRecorder()
	handler.ServeHTTP(liveness, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if liveness.Code != http.StatusOK || liveness.Body.String() != "ok\n" {
		t.Fatalf("liveness = status %d body %q", liveness.Code, liveness.Body.String())
	}
	beforeReady := httptest.NewRecorder()
	handler.ServeHTTP(beforeReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if beforeReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness before startup = %d", beforeReady.Code)
	}

	state.SetReady(true)
	afterReady := httptest.NewRecorder()
	handler.ServeHTTP(afterReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if afterReady.Code != http.StatusOK || afterReady.Body.String() != "ready\n" {
		t.Fatalf("readiness after startup = status %d body %q", afterReady.Code, afterReady.Body.String())
	}

	state.SetReady(false)
	duringShutdown := httptest.NewRecorder()
	handler.ServeHTTP(duringShutdown, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if duringShutdown.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness during shutdown = %d", duringShutdown.Code)
	}
}
