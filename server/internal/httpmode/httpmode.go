// Package httpmode holds the pure HTTP serving helpers for the server's
// --http mode (loopback Streamable HTTP + /health + bearer-token auth).
package httpmode

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// ValidateLoopback accepts only loopback host:port addresses
// (127.0.0.1, ::1, localhost) and returns the canonical "host:port" form.
func ValidateLoopback(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid addr %q: %w", addr, err)
	}
	if port == "" {
		return "", fmt.Errorf("invalid addr %q: missing port", addr)
	}
	switch host {
	case "127.0.0.1", "::1", "localhost":
	default:
		return "", fmt.Errorf("addr %q must be loopback (127.0.0.1 or ::1)", addr)
	}
	return net.JoinHostPort(host, port), nil
}

// Health is the /health response body.
type Health struct {
	OK      bool   `json:"ok"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Addr    string `json:"addr"`
}

// HealthHandler returns an unauthenticated GET /health handler.
func HealthHandler(version, addr string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Health{OK: true, Name: "screenreader", Version: version, Addr: addr})
	})
}
