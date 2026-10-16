package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// fault is an injected failure applied to every API request until it is cleared.
type fault struct {
	Status     int    `json:"status,omitempty"`      // HTTP status to answer with instead of a document
	Delay      string `json:"delay,omitempty"`       // Go duration to wait before answering
	RetryAfter string `json:"retry_after,omitempty"` // Retry-After header sent with Status

	delay time.Duration
}

func (f *fault) validate() error {
	if f.Status == 0 && f.Delay == "" {
		return fmt.Errorf("set status, delay or both")
	}
	if f.Status != 0 && (f.Status < 400 || f.Status > 599) {
		return fmt.Errorf("status %d is not a 4xx or 5xx code", f.Status)
	}
	if f.RetryAfter != "" && f.Status == 0 {
		return fmt.Errorf("retry_after needs a status")
	}
	if f.Delay != "" {
		d, err := time.ParseDuration(f.Delay)
		if err != nil || d < 0 {
			return fmt.Errorf("delay %q is not a non-negative Go duration", f.Delay)
		}
		f.delay = d
	}
	return nil
}

// faults holds the active fault, if any. Requests read it without locking.
type faults struct {
	current atomic.Pointer[fault]
}

// apply runs the active fault for one API request. It reports whether the request has been answered
// (or abandoned by the client) and must not be served normally.
func (fs *faults) apply(ctx context.Context, w http.ResponseWriter) bool {
	f := fs.current.Load()
	if f == nil {
		return false
	}
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return true
		case <-timer.C:
		}
	}
	if f.Status == 0 {
		return false
	}
	if f.RetryAfter != "" {
		w.Header().Set("Retry-After", f.RetryAfter)
	}
	http.Error(w, "fault injected: "+strconv.Itoa(f.Status), f.Status)
	return true
}

// ServeHTTP implements /admin/fault: GET shows the active fault, PUT replaces it, DELETE clears it.
func (fs *faults) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		f := fs.current.Load()
		if f == nil {
			f = &fault{}
		}
		_ = json.NewEncoder(w).Encode(f)
	case http.MethodPut:
		var f fault
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&f); err != nil {
			http.Error(w, "invalid fault: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := f.validate(); err != nil {
			http.Error(w, "invalid fault: "+err.Error(), http.StatusBadRequest)
			return
		}
		fs.current.Store(&f)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		fs.current.Store(nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
