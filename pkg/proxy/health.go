package proxy

import (
	"net/http"
	"sync/atomic"
)

type HealthState struct {
	ready atomic.Bool
}

func (state *HealthState) SetReady(ready bool) {
	state.ready.Store(ready)
}

func (state *HealthState) Ready() bool {
	return state.ready.Load()
}

func NewHealthHandler(state *HealthState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if state.Ready() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ready\n"))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not ready\n"))
	})
	return mux
}
