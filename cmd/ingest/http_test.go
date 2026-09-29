package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestMux(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		ready bool
		want  int
	}{
		{name: "healthz when not ready", path: "/healthz", ready: false, want: http.StatusOK},
		{name: "readyz when ready", path: "/readyz", ready: true, want: http.StatusOK},
		{name: "readyz when not ready", path: "/readyz", ready: false, want: http.StatusServiceUnavailable},
		{name: "metrics", path: "/metrics", ready: true, want: http.StatusOK},
		{name: "unknown path", path: "/nope", ready: true, want: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ready atomic.Bool
			ready.Store(tt.ready)

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil)
			newMux(&ready).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Errorf("GET %s: got status %d, want %d", tt.path, rec.Code, tt.want)
			}
		})
	}
}
