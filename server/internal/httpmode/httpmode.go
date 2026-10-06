// Package httpmode holds the pure HTTP serving helpers for the server's
// --http mode (loopback Streamable HTTP + /health + bearer-token auth).
package httpmode

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
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

// TokenSource returns the current bearer token ("" = unauthenticated).
// It is consulted on every request so config edits rotate the token live.
type TokenSource func() string

// NewTokenAuth wraps next, enforcing "Authorization: Bearer <token>" whenever
// source returns a non-empty token. With an empty token, requests pass through
// unauthenticated (HTTP mode stays usable without auth).
func NewTokenAuth(source TokenSource, next http.Handler) http.Handler {
	verifier := func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		want := source()
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(token)) != 1 {
			// Must wrap auth.ErrInvalidToken for the middleware to answer 401
			// (a plain error maps to 500 in go-sdk v1.8.0).
			return nil, auth.ErrInvalidToken
		}
		// Static local token: report a fresh expiry the middleware accepts.
		return &auth.TokenInfo{Expiration: time.Now().Add(time.Hour)}, nil
	}
	authed := auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{})(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if source() == "" {
			next.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}
