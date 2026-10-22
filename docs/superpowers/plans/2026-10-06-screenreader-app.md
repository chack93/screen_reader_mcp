# ScreenReader App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Superseded 2026-10-20 — OCR removed.** Any step below that touches the `ocr`/`click_text`/`wait_for_text` tools or the read-only tool list is stale: those tools are gone, full access is 15 tools, and read-only is `capture`, `window_info`, `list_windows`, `list_screens`, `doctor`, `wait`. The app's own behavior (TCC ownership, HTTP hosting, target picking) is unaffected.

**Goal:** A macOS menu-bar app (Swift/AppKit) that owns the TCC permissions, hosts the existing Go MCP server in a new loopback Streamable-HTTP mode, and gives any local agent a stable MCP endpoint — with in-app window-picking and rectangle drawing for target selection.

**Architecture:** The Go server gains a `--http` flag serving MCP over Streamable HTTP on `127.0.0.1:18427/mcp` (SDK `mcp.NewStreamableHTTPHandler`, bearer token via `auth.RequireBearerToken`, plus an unauthenticated `/health`) alongside its unchanged stdio mode. A new Swift app (`app/`) spawns the Go binary — embedded in its bundle — with `--http`, supervises it (restart/backoff), owns the three TCC permissions, and writes the shared `config.json` that the Go server re-reads per action. The app is a host, never a proxy: agents talk HTTP directly to the Go server.

**Tech Stack:** Go (module `screenreader`, SDK `github.com/modelcontextprotocol/go-sdk v1.8.0`, cgo via robotgo + tmc/apple — unchanged); Swift 5.9+/AppKit via Swift Package Manager + `build.sh` bundle assembly + `codesign`; macOS 13+ (dev box runs 27.0).

**Spec:** `docs/superpowers/specs/2026-10-06-screenreader-app-design.md` — read it first; this plan argues from it. `2026-10-03-screenreader-mcp-design.md` stays authoritative for Go engine internals (tools, coordinate model).

**Plan-time refinements to the spec:** (1) the Swift side is a **Swift Package + `build.sh`** that assembles and signs the `.app`, instead of a committed `ScreenReader.xcodeproj` — identical AppKit result, but `swift test` runs in seconds (spike-verified); (2) the HTTP handler sets `StreamableHTTPOptions{JSONResponse: true}`, so responses are plain JSON (the in-app client still tolerates SSE bodies); (3) the TCC-attribution canary (spec §7) runs at **Task 14** — the earliest point the app can spawn the server and exercise a real capture — so no UI is built on top of a broken assumption.

## Global Constraints

- Coordinates: **screen-absolute, top-left origin, Y-down** everywhere. The rect the app draws must be saved in this space (`RectMath.toScreenAbsolute`).
- Config: single source of truth at `~/.config/screenreader/config.json`. The app mutates **only** `app`, `title`, `mode`, `rect`, `http`; it must **preserve all other keys**; writes are atomic (temp file + rename); malformed JSON is rejected, never overwritten.
- HTTP: **loopback only** (`127.0.0.1` / `::1` / `localhost`; anything else refused at startup). Default addr `127.0.0.1:18427`. `/mcp` enforces `Authorization: Bearer <token>` whenever `http.token` is non-empty (re-read from config **per request** — live rotation); `/health` is unauthenticated.
- Go stdio mode (no `--http`) stays exactly as today: same 17 tools, same startup, same config behavior.
- Gate every Go task with: `CGO_ENABLED=1 go vet ./...` + `CGO_ENABLED=1 go test ./...` (from `server/`); keep code gofmt-clean.
- Gate every Swift-Kit task with `cd app && swift test`; the app targets with `cd app && swift build`. Keep formatting as-authored.
- Swift: deployment target macOS 13; app is `LSUIElement` (menu-bar only, no dock icon); bundle ID `dev.chack.screenreader`; executable `ScreenReader`.
- The app embeds the Go binary at `ScreenReader.app/Contents/Resources/screenreader` and launches it with the single argument `--http`.
- TDD for all Go and Swift-Kit logic: failing test first → see it fail → minimal implementation → see it pass → commit.
- Commit after every task; conventional messages (`feat:` / `test:` / `docs:` / `chore:`), matching `git log`.

---

### Task 1: Go — config `http` section

**Files:**
- Modify: `server/internal/config/config.go`
- Test: `server/internal/config/config_test.go`

**Interfaces:**
- Consumes: existing `Config`, `Defaults() *Config`, `Load(path) (*Config, error)`, `Save(path, *Config)`, `DefaultPath()` (see config.go); the test helper `tempDir(t)` redirects `$HOME` (config_test.go:10).
- Produces: `config.HTTPCfg{Addr string; Token string}`; `Config.HTTP HTTPCfg` (json `http`); default `Addr = "127.0.0.1:18427"`, `Token = ""`. Tasks 3/4 and `build.sh`/Swift consumers rely on these exact names.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/config/config_test.go`:

```go
func TestDefaults_http(t *testing.T) {
	c := Defaults()
	if c.HTTP.Addr != "127.0.0.1:18427" || c.HTTP.Token != "" {
		t.Fatalf("unexpected http defaults: %+v", c.HTTP)
	}
}

func TestLoad_httpOverride(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"http":{"addr":"127.0.0.1:19999","token":"abc"}}`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if c.HTTP.Addr != "127.0.0.1:19999" || c.HTTP.Token != "abc" {
		t.Fatalf("http not loaded: %+v", c.HTTP)
	}
	if c.Mode != "app" || c.Capture.MaxWidth != 1600 {
		t.Fatalf("defaults not filled: %+v", c)
	}
}

func TestSaveLoad_httpRoundTrip(t *testing.T) {
	tempDir(t)
	p := filepath.Join(t.TempDir(), "config.json")
	c := Defaults()
	c.HTTP.Token = "t0k"
	if err := Save(p, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.HTTP != c.HTTP {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got.HTTP, c.HTTP)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd server && CGO_ENABLED=1 go test ./internal/config/ -run http -v`
Expected: build FAIL — `c.HTTP undefined (type *Config has no field or method HTTP)`.

- [ ] **Step 3: Implement**

In `server/internal/config/config.go`, after the `InputCfg` struct add:

```go
type HTTPCfg struct {
	Addr  string `json:"addr"`  // loopback only, e.g. "127.0.0.1:18427"
	Token string `json:"token"` // bearer token for /mcp; empty = unauthenticated
}
```

In `Config`, after the `Input` field add:

```go
	HTTP    HTTPCfg    `json:"http"`
```

In `Defaults()`, after `Input:` add:

```go
		HTTP:    HTTPCfg{Addr: "127.0.0.1:18427"},
```

Then `gofmt -w internal/config/config.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...`
Expected: all PASS (existing config tests included).

- [ ] **Step 5: Commit**

```bash
git add server/internal/config/
git commit -m "feat(config): add http section (loopback addr + bearer token)"
```

---

### Task 2: Go — `internal/httpmode`: ValidateLoopback + HealthHandler

**Files:**
- Create: `server/internal/httpmode/httpmode.go`
- Test: `server/internal/httpmode/httpmode_test.go`

**Interfaces:**
- Consumes: stdlib only (this task adds no new dependency).
- Produces:
  - `httpmode.ValidateLoopback(addr string) (string, error)` — canonical `host:port` only for loopback hosts.
  - `httpmode.Health{OK bool; Name, Version, Addr string}` and `httpmode.HealthHandler(version, addr string) http.Handler` — GET-only, JSON body.
  - Task 3 consumes both; Task 4 adds `NewTokenAuth` to this package.

- [ ] **Step 1: Write the failing test**

Create `server/internal/httpmode/httpmode_test.go`:

```go
package httpmode

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateLoopback(t *testing.T) {
	ok := map[string]string{
		"127.0.0.1:18427": "127.0.0.1:18427",
		"localhost:1":     "localhost:1",
		"[::1]:2":         "[::1]:2",
	}
	for in, want := range ok {
		got, err := ValidateLoopback(in)
		if err != nil || got != want {
			t.Fatalf("ValidateLoopback(%q) = %q, %v; want %q, nil", in, got, err, want)
		}
	}
	for _, in := range []string{"0.0.0.0:1", "192.168.1.5:1", "127.0.0.1", "notanaddr", ""} {
		if _, err := ValidateLoopback(in); err == nil {
			t.Fatalf("ValidateLoopback(%q) should error", in)
		}
	}
}

func TestHealthHandler(t *testing.T) {
	ts := httptest.NewServer(HealthHandler("9.9.9", "127.0.0.1:18427"))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var h Health
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatal(err)
	}
	if !h.OK || h.Name != "screenreader" || h.Version != "9.9.9" || h.Addr != "127.0.0.1:18427" {
		t.Fatalf("bad health body: %+v", h)
	}

	resp2, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST status %d, want 405", resp2.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd server && CGO_ENABLED=1 go test ./internal/httpmode/`
Expected: FAIL — package does not exist / undefined: ValidateLoopback.

- [ ] **Step 3: Implement**

Create `server/internal/httpmode/httpmode.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && CGO_ENABLED=1 go vet ./internal/httpmode/ && CGO_ENABLED=1 go test ./internal/httpmode/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/httpmode/
git commit -m "feat(httpmode): loopback addr validation + /health handler"
```

---

### Task 3: Go — `internal/httpmode`: NewTokenAuth (SDK bearer middleware)

**Files:**
- Modify: `server/internal/httpmode/httpmode.go`
- Test: `server/internal/httpmode/httpmode_test.go`

**Interfaces:**
- Consumes: `github.com/modelcontextprotocol/go-sdk/auth` (already in the module graph via go-sdk). Verified signatures: `auth.RequireBearerToken(verifier, *auth.RequireBearerTokenOptions) func(http.Handler) http.Handler`; `auth.TokenVerifier = func(ctx, token string, req *http.Request) (*auth.TokenInfo, error)`; `auth.TokenInfo{Expiration time.Time, ...}` — the middleware rejects a zero `Expiration` unless `AllowMissingExpiration` is set.
- Produces: `httpmode.TokenSource func() string` and `httpmode.NewTokenAuth(source TokenSource, next http.Handler) http.Handler` — empty token ⇒ passthrough; non-empty ⇒ `Authorization: Bearer <token>` required (constant-time compare, re-read per request). Task 4 consumes it.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/httpmode/httpmode_test.go`:

```go
func TestNewTokenAuth(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	var tok string // the live token source
	ts := httptest.NewServer(NewTokenAuth(func() string { return tok }, inner))
	defer ts.Close()

	// no token configured → passthrough without any header
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("passthrough status %d", resp.StatusCode)
	}

	// token appears in config → next request must be authenticated
	tok = "sekret"
	r1, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	r1.Body.Close()
	if r1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing header: status %d, want 401", r1.StatusCode)
	}

	req, _ := http.NewRequest("GET", ts.URL, nil)
	req.Header.Set("Authorization", "Bearer wrong")
	r2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()
	if r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: status %d, want 401", r2.StatusCode)
	}

	req.Header.Set("Authorization", "Bearer sekret")
	r3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r3.Body.Close()
	if r3.StatusCode != 200 {
		t.Fatalf("good token: status %d, want 200", r3.StatusCode)
	}

	// token removed again → passthrough (live rotation both ways)
	tok = ""
	r4, err := http.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	r4.Body.Close()
	if r4.StatusCode != 200 {
		t.Fatalf("revoked-token passthrough: status %d", r4.StatusCode)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd server && CGO_ENABLED=1 go test ./internal/httpmode/ -run TestNewTokenAuth`
Expected: FAIL — undefined: NewTokenAuth.

- [ ] **Step 3: Implement**

Append to `server/internal/httpmode/httpmode.go` (and extend its import block with `"context"`, `"crypto/subtle"`, `"time"`, `"github.com/modelcontextprotocol/go-sdk/auth"`):

```go
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
			return nil, auth.ErrInvalidToken // SDK maps THIS error to 401; others → 500 (T3 finding)
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./internal/httpmode/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/httpmode/
git commit -m "feat(httpmode): bearer-token middleware with live token source"
```

---

### Task 4: Go — `--http` mode in main + full-stack test + README

**Files:**
- Modify: `server/main.go` (whole file, below)
- Create: `server/main_http_test.go`
- Modify: `README.md` (new section after "## opencode config")

**Interfaces:**
- Consumes: `config.Load/DefaultPath/HTTP`, `httpmode.ValidateLoopback/HealthHandler/NewTokenAuth`, `tools.RegisterReading/Input/Target`, `mcp.NewStreamableHTTPHandler`.
- Produces: binary flag `--http`; `const version = "0.1.0"`; `func buildServer() *mcp.Server`; `func newHTTPMux(addr string) http.Handler` (package main — used by tests); `GET http://<addr>/health`; `POST http://<addr>/mcp` (Streamable HTTP, JSON responses, bearer-gated when configured).

- [ ] **Step 1: Write the failing test**

Create `server/main_http_test.go`:

```go
package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"screenreader/internal/config"
)

func mcpPost(t *testing.T, ts *httptest.Server, session, body string) (http.Header, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp.Header, map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return resp.Header, out
}

func TestHTTPFullStack(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // clean config ⇒ no token
	ts := httptest.NewServer(newHTTPMux("127.0.0.1:18427"))
	defer ts.Close()

	// /health answers without auth
	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status %d", resp.StatusCode)
	}

	// MCP initialize handshake
	hdr, msg := mcpPost(t, ts, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	session := hdr.Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("no Mcp-Session-Id header in initialize response")
	}
	result, _ := msg["result"].(map[string]any)
	info, _ := result["serverInfo"].(map[string]any)
	if info["name"] != "screenreader" {
		t.Fatalf("serverInfo: %v", result)
	}

	// initialized notification, then tools/list
	mcpPost(t, ts, session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	_, msg = mcpPost(t, ts, session, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	result, _ = msg["result"].(map[string]any)
	got, _ := result["tools"].([]any)
	if len(got) != 17 {
		var names []string
		for _, tx := range got {
			m, _ := tx.(map[string]any)
			names = append(names, m["name"].(string))
		}
		t.Fatalf("got %d tools: %v", len(got), strings.Join(names, ","))
	}
}

func TestHTTPMux_tokenEnforced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Defaults()
	cfg.HTTP.Token = "tok123"
	if err := config.Save(config.DefaultPath(), cfg); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(newHTTPMux("127.0.0.1:18427"))
	defer ts.Close()

	// no header → 401
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-header status %d, want 401", resp.StatusCode)
	}

	// correct header → initialize succeeds
	req2, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Accept", "application/json, text/event-stream")
	req2.Header.Set("Authorization", "Bearer tok123")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("correct-token status %d: %s", resp2.StatusCode, b)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd server && CGO_ENABLED=1 go test .`
Expected: FAIL — undefined: newHTTPMux.

- [ ] **Step 3: Implement**

Replace `server/main.go` entirely:

```go
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"screenreader/internal/config"
	"screenreader/internal/httpmode"
	"screenreader/internal/tools"
)

const version = "0.1.0"

// buildServer constructs the MCP server with all tools registered (stdio and HTTP share it).
func buildServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "screenreader", Version: version}, nil)
	tools.RegisterReading(server)
	tools.RegisterInput(server)
	tools.RegisterTarget(server)
	return server
}

// newHTTPMux wires /health (unauthenticated) + /mcp (Streamable HTTP, JSON
// responses, bearer-gated whenever http.token is set). The token source
// re-reads config per request so edits rotate it live.
func newHTTPMux(addr string) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return buildServer() },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	)
	mux := http.NewServeMux()
	mux.Handle("/health", httpmode.HealthHandler(version, addr))
	mux.Handle("/mcp", httpmode.NewTokenAuth(func() string {
		c, _ := config.Load(config.DefaultPath())
		return c.HTTP.Token
	}, streamable))
	return mux
}

func main() {
	httpMode := flag.Bool("http", false, "serve MCP over Streamable HTTP on the configured loopback addr (config http.addr) instead of stdio")
	flag.Parse()

	if !*httpMode {
		if err := buildServer().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("server: %v", err)
		}
		return
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	addr, err := httpmode.ValidateLoopback(cfg.HTTP.Addr)
	if err != nil {
		log.Fatalf("http: %v", err)
	}
	if cfg.HTTP.Token == "" {
		log.Printf("WARNING: http.token is empty — /mcp is unauthenticated. Set http.token in config.")
	}

	log.Printf("screenreader serving MCP over Streamable HTTP on http://%s/mcp", addr)
	if err := http.ListenAndServe(addr, newHTTPMux(addr)); err != nil {
		log.Fatalf("http: %v", err)
	}
}
```

- [ ] **Step 4: Run the full gate**

Run: `cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./...`
Expected: all PASS (stdio behavior unchanged; existing suites green).

- [ ] **Step 5: Manual smoke (no permissions needed)**

```bash
cd server && CGO_ENABLED=1 go build -o screenreader . && ./screenreader --http &
sleep 1
curl -s http://127.0.0.1:18427/health
# expect: {"ok":true,"name":"screenreader","version":"0.1.0","addr":"127.0.0.1:18427"}
kill %1
```

Note: if the real config has a non-empty `http.token`, `/health` still answers (it is unauthenticated); only `/mcp` requires the header.

- [ ] **Step 6: Document in README**

After the "## opencode config" section in `README.md`, insert:

````markdown
## HTTP mode (manual)

Besides stdio, the server can serve MCP over **Streamable HTTP** on a loopback address:

```bash
cd server && CGO_ENABLED=1 go build -o screenreader .
./screenreader --http
# → screenreader serving MCP over Streamable HTTP on http://127.0.0.1:18427/mcp
```

The address and bearer token come from the `http` section of `~/.config/screenreader/config.json`:

```json
"http": { "addr": "127.0.0.1:18427", "token": "..." }
```

- `addr` must be loopback (127.0.0.1 or ::1); anything else is refused at startup.
- With `token` set, clients must send `Authorization: Bearer <token>`; token edits apply on the next request. Without a token the endpoint is unauthenticated (a warning is logged).
- `GET /health` reports liveness without auth (the menu-bar app uses it).
- In opencode: `"type": "remote"`, `"url": "http://127.0.0.1:18427/mcp"`, `"oauth": false`, plus the `Authorization` header when a token is set.

The recommended way to run HTTP mode is the ScreenReader menu-bar app (see below).
````

- [ ] **Step 7: Commit**

```bash
git add server/main.go server/main_http_test.go README.md
git commit -m "feat: --http mode — Streamable HTTP endpoint with /health + bearer token"
```

---

### Task 5: App foundation — Swift package + minimal menu-bar app + build.sh

**Files:**
- Create: `app/Package.swift`
- Create: `app/Info.plist`
- Create: `app/Sources/ScreenReaderKit/Kit.swift`
- Create: `app/Tests/ScreenReaderKitTests/KitTests.swift`
- Create: `app/Sources/ScreenReader/main.swift`
- Create: `app/Sources/ScreenReader/AppDelegate.swift`
- Create: `build.sh` (repo root)
- Modify: `.gitignore`

**Interfaces:**
- Consumes: `server/screenreader` binary (Task 4 build).
- Produces: `./build.sh` → `app/build/ScreenReader.app` (signed, with the Go binary embedded); Swift targets `ScreenReaderKit` (pure logic, no AppKit) + `ScreenReader` (executable) + `ScreenReaderKitTests`; running the app shows a menu-bar icon with a **Quit ScreenReader** item.

- [ ] **Step 1: Package.swift**

Create `app/Package.swift`:

```swift
// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "ScreenReader",
    platforms: [.macOS(.v13)],
    targets: [
        .target(name: "ScreenReaderKit"),
        .executableTarget(
            name: "ScreenReader",
            dependencies: ["ScreenReaderKit"]
        ),
        .testTarget(
            name: "ScreenReaderKitTests",
            dependencies: ["ScreenReaderKit"]
        ),
    ]
)
```

(Default `Sources/<target>` / `Tests/<target>` layout — no path overrides needed.)

- [ ] **Step 2: Kit placeholder + first test (proves `swift test`)**

Create `app/Sources/ScreenReaderKit/Kit.swift`:

```swift
import Foundation

/// Marker for the pure-logic library (no AppKit here).
public enum Kit {
    public static let version = "0.1.0"
}
```

Create `app/Tests/ScreenReaderKitTests/KitTests.swift`:

```swift
import XCTest
@testable import ScreenReaderKit

final class KitTests: XCTestCase {
    func testVersion() {
        XCTAssertEqual(Kit.version, "0.1.0")
    }
}
```

Run: `cd app && swift test` → Expected: PASS (~seconds).

- [ ] **Step 3: Minimal AppKit app**

Create `app/Sources/ScreenReader/AppDelegate.swift`:

```swift
import AppKit

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.title = "SR"
        let menu = NSMenu()
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        statusItem.menu = menu
    }
}
```

Create `app/Sources/ScreenReader/main.swift`:

```swift
import AppKit

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory) // menu-bar only, no dock icon
if CommandLine.arguments.contains("--smoke") {
    // Build-verification mode: shows the item briefly, exits cleanly.
    DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { NSApp.terminate(nil) }
}
app.run()
```

- [ ] **Step 4: Info.plist (used by build.sh when assembling the bundle)**

Create `app/Info.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>ScreenReader</string>
  <key>CFBundleDisplayName</key><string>ScreenReader</string>
  <key>CFBundleIdentifier</key><string>dev.chack.screenreader</string>
  <key>CFBundleExecutable</key><string>ScreenReader</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSAppleEventsUsageDescription</key>
  <string>ScreenReader uses System Events to list and focus windows.</string>
</dict>
</plist>
```

- [ ] **Step 5: build.sh**

Create `build.sh` (repo root), `chmod +x build.sh`:

```bash
#!/usr/bin/env bash
# Builds the Go server and the Swift app, assembles ScreenReader.app with the
# Go binary embedded, and codesigns both. Requires Go + Xcode toolchain.
# Optional (recommended, one-time): a self-signed codesigning identity named
# "ScreenReader Dev" — without it signing is ad-hoc and TCC grants reset on
# every rebuild (see README "ScreenReader app" section).
set -euo pipefail
cd "$(dirname "$0")"

# 1. Go server binary
(cd server && CGO_ENABLED=1 go build -o screenreader .)

# 2. Swift app binary (release)
(cd app && swift build -c release)

# 3. Assemble the .app bundle
APP="app/build/ScreenReader.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp app/.build/release/ScreenReader "$APP/Contents/MacOS/ScreenReader"
cp app/Info.plist "$APP/Contents/Info.plist"
cp server/screenreader "$APP/Contents/Resources/screenreader"

# 4. Codesign (stable identity when available, else ad-hoc with a warning)
IDENTITY="ScreenReader Dev"
if security find-identity -v -p codesigning | grep -q "$IDENTITY"; then
  SIGN=(--force --sign "$IDENTITY")
else
  echo "warning: no '$IDENTITY' codesigning identity — using AD-HOC signing."
  echo "         TCC grants will NOT survive app rebuilds. Create the identity"
  echo "         once via Keychain Access → Certificate Assistant → Create a"
  echo "         New Certificate (name '$IDENTITY', kind Code Signing, self-signed)."
  SIGN=(--force --sign -)
fi
codesign "${SIGN[@]}" "$APP/Contents/Resources/screenreader"
codesign "${SIGN[@]}" "$APP"

echo "built: $APP"
echo "run:   open $APP"
```

- [ ] **Step 6: gitignore**

Append to `.gitignore`:

```
app/.build/
app/build/
```

- [ ] **Step 7: Full build + smoke verification**

```bash
./build.sh
# expect: "built: app/build/ScreenReader.app"
codesign -dv app/build/ScreenReader.app 2>&1 | grep -E "Identifier|flags"
# expect: Identifier=dev.chack.screenreader
app/build/ScreenReader.app/Contents/MacOS/ScreenReader --smoke
# expect: exits 0 after ~1.5s; the menu bar shows "SR" briefly
```

- [ ] **Step 8: Commit**

```bash
git add app/ build.sh .gitignore
git commit -m "feat(app): Swift package skeleton — menu-bar app, bundle build script"
```

---

### Task 6: One-time self-signed codesigning identity (user action)

**Files:** none (keychain configuration + verification)

**Interfaces:**
- Consumes: `build.sh` identity detection (Task 5).
- Produces: a `ScreenReader Dev` codesigning identity in the login keychain so TCC grants survive app rebuilds (spec §4.6).

- [x] **Step 1: PAUSE — ask the user to create the identity**

  **2026-10-07 docs(plan) correction — this CAN be scripted**, done controller-side with user go-ahead (no GUI):
  `openssl req -x509 -newkey rsa:2048 -nodes -keyout key.pem -out cert.pem -days 3650 -subj "/CN=ScreenReader Dev"
  -addext "basicConstraints=critical,CA:true" -addext "keyUsage=critical,digitalSignature"
  -addext "extendedKeyUsage=critical,codeSigning"` → re-export the p12 with `-legacy -certpbe PBE-SHA1-3DES
  -keypbe PBE-SHA1-3DES -macalg sha1` (OpenSSL ≥3 defaults break `security import` MAC verification) →
  `security import sr.p12 -k ~/Library/Keychains/login.keychain-db -P pw -T /usr/bin/codesign` →
  `security add-trusted-cert -p codeSign cert.pem`.

Tell the user (this cannot be scripted): Keychain Access → menu **Certificate Assistant → Create a New Certificate…** → Name: `ScreenReader Dev`, Certificate Type: **Code Signing**, Identity Type: **Self Signed Certificate**, Where to Save: **Login** keychain.

Wait for confirmation before continuing. (If the user skips it, everything still works — but permissions must be re-granted after every app rebuild.)

- [x] **Step 2: Verify** — identity valid; build → `Authority=ScreenReader Dev`, DR = `identifier "dev.chack.screenreader" and certificate leaf = H"b705e67e…"`; TCC csreq blobs cert-bound (SHA-1, no cdhash); forced-cdhash-change rebuild → grants survived, capture returned live pixels, zero re-grants.

```bash
security find-identity -v -p codesigning | grep "ScreenReader Dev"
# expect a line ending in "ScreenReader Dev"
./build.sh
codesign -dvv app/build/ScreenReader.app 2>&1 | grep -v "^Executable" | head -6
# expect Authority=ScreenReader Dev (NOT "flags=0x2(adhoc)")
```

- [x] **Step 3: No commit** (no files change — environment step only; proceed to Task 7).

---

### Task 7: Swift Kit — ConfigStore

**Files:**
- Create: `app/Sources/ScreenReaderKit/ConfigStore.swift`
- Test: `app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift`

**Interfaces:**
- Consumes: nothing (Foundation + Security only).
- Produces: `ConfigStore` with `static let defaultPath: String`, `struct Target {var app,title,mode: String; var x,y,w,h: Int}`, `enum ConfigError {case unparseable, notObject}`, `static func load(path:) throws -> [String: Any]`, `static func save(_:path:) throws`, `static func applyTarget(_:to:) -> [String: Any]`, `static func target(from:) -> Target`, `static func httpConfig(from:) -> (addr: String, token: String)`, `static func ensureToken(_:in:) -> [String: Any]`, `static func generateToken() -> String`. Tasks 13 (token), 14 (menu), 16 (settings) consume these exact names.

- [ ] **Step 1: Write the failing tests**

Create `app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift`:

```swift
import XCTest
@testable import ScreenReaderKit

final class ConfigStoreTests: XCTestCase {
    func tmpConfig() -> String {
        NSTemporaryDirectory() + "sr-cfg-\(UUID().uuidString).json"
    }

    func testLoad_missingReturnsEmpty() throws {
        // (T7 finding: XCTAssertEqual can't compile for [String: Any] — not Equatable; isEmpty asserts the same intent)
        XCTAssertTrue(try ConfigStore.load(path: "/nonexistent/xyz.json").isEmpty)
    }

    func testLoad_unparseableThrows() throws {
        let p = tmpConfig()
        try "not json {{{".write(toFile: p, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(atPath: p) }
        XCTAssertThrowsError(try ConfigStore.load(path: p))
    }

    func testApplyTarget_preservesOtherKeys() {
        let json: [String: Any] = [
            "version": 1,
            "ocr": ["languages": ["en"], "level": "accurate"],
            "input": ["delay_ms": 120],
            "app": "OldApp",
        ]
        let out = ConfigStore.applyTarget(
            ConfigStore.Target(app: "Finder", title: "Files", mode: "app", x: 0, y: 0, w: 0, h: 0),
            to: json)
        XCTAssertEqual(out["app"] as? String, "Finder")
        XCTAssertEqual(out["title"] as? String, "Files")
        XCTAssertEqual(out["mode"] as? String, "app")
        XCTAssertEqual(out["version"] as? Int, 1)
        let ocr = out["ocr"] as? [String: Any]
        XCTAssertEqual(ocr?["level"] as? String, "accurate")
        let input = out["input"] as? [String: Any]
        XCTAssertEqual(input?["delay_ms"] as? Int, 120)
    }

    func testTargetRoundTrip() {
        let t = ConfigStore.Target(app: "A", title: "T", mode: "rect", x: 10, y: 20, w: 300, h: 200)
        XCTAssertEqual(ConfigStore.target(from: ConfigStore.applyTarget(t, to: [:])), t)
    }

    func testHTTPConfigDefaults() {
        XCTAssertEqual(ConfigStore.httpConfig(from: [:]).addr, "127.0.0.1:18427")
        XCTAssertEqual(ConfigStore.httpConfig(from: [:]).token, "")
        let j = ["http": ["addr": "127.0.0.1:1", "token": "t"]]
        XCTAssertEqual(ConfigStore.httpConfig(from: j).addr, "127.0.0.1:1")
        XCTAssertEqual(ConfigStore.httpConfig(from: j).token, "t")
    }

    func testEnsureToken() {
        let set = ConfigStore.ensureToken("tok", in: ["app": "A"])
        XCTAssertEqual((set["http"] as? [String: Any])?["token"] as? String, "tok")
        XCTAssertEqual(set["app"] as? String, "A")
        // already present → untouched
        let keep = ConfigStore.ensureToken("new", in: ["http": ["token": "old"]])
        XCTAssertEqual((keep["http"] as? [String: Any])?["token"] as? String, "old")
    }

    func testSaveLoadRoundTrip() throws {
        let p = tmpConfig()
        defer { try? FileManager.default.removeItem(atPath: p) }
        var json = ConfigStore.applyTarget(
            ConfigStore.Target(app: "X", title: "", mode: "app", x: 0, y: 0, w: 0, h: 0), to: [:])
        json["keep"] = ["nested": true]
        try ConfigStore.save(json, path: p)
        let back = try ConfigStore.load(path: p)
        XCTAssertEqual(back["app"] as? String, "X")
        XCTAssertEqual((back["keep"] as? [String: Any])?["nested"] as? Bool, true)
    }

    func testSaveCreatesParentDirs() throws {
        let p = NSTemporaryDirectory() + "sr-nested-\(UUID().uuidString)/a/b/config.json"
        defer { try? FileManager.default.removeItem(atPath: p) }
        try ConfigStore.save(["version": 1], path: p)
        XCTAssertEqual(try ConfigStore.load(path: p)["version"] as? Int, 1)
    }

    func testGenerateToken() {
        let t = ConfigStore.generateToken()
        XCTAssertEqual(t.count, 64)
        XCTAssertTrue(t.allSatisfy { "0123456789abcdef".contains($0) })
        XCTAssertNotEqual(t, ConfigStore.generateToken())
    }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter ConfigStoreTests`
Expected: compile error — cannot find `ConfigStore` (or all tests fail).

- [ ] **Step 3: Implement**

Create `app/Sources/ScreenReaderKit/ConfigStore.swift`:

```swift
import Foundation
import Security

/// Reads/writes ~/.config/screenreader/config.json.
/// Mutates only app/title/mode/rect/http; preserves every other key.
public enum ConfigStore {
    public static let defaultPath: String =
        NSHomeDirectory() + "/.config/screenreader/config.json"

    public struct Target: Equatable {
        public var app: String
        public var title: String
        public var mode: String // "app" | "rect"
        public var x: Int
        public var y: Int
        public var w: Int
        public var h: Int
        public init(app: String, title: String, mode: String, x: Int, y: Int, w: Int, h: Int) {
            self.app = app; self.title = title; self.mode = mode
            self.x = x; self.y = y; self.w = w; self.h = h
        }
    }

    public enum ConfigError: Error { case unparseable, notObject }

    /// Loads the raw JSON object. A missing file returns [:]. Malformed JSON THROWS
    /// (callers must not overwrite a damaged config).
    public static func load(path: String = defaultPath) throws -> [String: Any] {
        guard let data = FileManager.default.contents(atPath: path) else { return [:] }
        let obj: Any
        do { obj = try JSONSerialization.jsonObject(with: data) }
        catch { throw ConfigError.unparseable }
        guard let dict = obj as? [String: Any] else { throw ConfigError.notObject }
        return dict
    }

    /// Atomically rewrites the file (temp + rename, parent dirs created).
    public static func save(_ json: [String: Any], path: String = defaultPath) throws {
        let data: Data
        do { data = try JSONSerialization.data(withJSONObject: json, options: [.prettyPrinted, .sortedKeys]) }
        catch { throw ConfigError.notObject }
        let tmp = path + ".sr-tmp"
        try FileManager.default.createDirectory(
            atPath: (path as NSString).deletingLastPathComponent, withIntermediateDirectories: true)
        do {
            try data.write(to: URL(fileURLWithPath: tmp), options: .atomic)
            if FileManager.default.fileExists(atPath: path) {
                try FileManager.default.removeItem(atPath: path)
            }
            try FileManager.default.moveItem(atPath: tmp, toPath: path)
        } catch {
            try? FileManager.default.removeItem(atPath: tmp)
            throw ConfigError.notObject
        }
    }

    public static func applyTarget(_ t: Target, to json: [String: Any]) -> [String: Any] {
        var out = json
        out["app"] = t.app
        out["title"] = t.title
        out["mode"] = t.mode
        out["rect"] = ["x": t.x, "y": t.y, "w": t.w, "h": t.h]
        return out
    }

    public static func target(from json: [String: Any]) -> Target {
        let rect = json["rect"] as? [String: Any] ?? [:]
        func i(_ k: String) -> Int { rect[k] as? Int ?? 0 }
        return Target(app: json["app"] as? String ?? "",
                      title: json["title"] as? String ?? "",
                      mode: json["mode"] as? String ?? "app",
                      x: i("x"), y: i("y"), w: i("w"), h: i("h"))
    }

    public static func httpConfig(from json: [String: Any]) -> (addr: String, token: String) {
        let http = json["http"] as? [String: Any] ?? [:]
        return (http["addr"] as? String ?? "127.0.0.1:18427",
                http["token"] as? String ?? "")
    }

    /// Sets http.token only when missing/empty; preserves everything else.
    public static func ensureToken(_ token: String, in json: [String: Any]) -> [String: Any] {
        var out = json
        var http = out["http"] as? [String: Any] ?? [:]
        if (http["token"] as? String).map({ $0.isEmpty }) ?? true {
            http["token"] = token
            out["http"] = http
        }
        return out
    }

    /// 32 random bytes, lower-case hex (64 chars).
    public static func generateToken() -> String {
        var bytes = [UInt8](repeating: 0, count: 32)
        _ = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        return bytes.map { String(format: "%02x", $0) }.joined()
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift test --filter ConfigStoreTests`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/ConfigStore.swift app/Tests/ScreenReaderKitTests/ConfigStoreTests.swift
git commit -m "feat(app): ConfigStore — key-preserving atomic config.json edits"
```

---

### Task 8: Swift Kit — RectMath

**Files:**
- Create: `app/Sources/ScreenReaderKit/RectMath.swift`
- Test: `app/Tests/ScreenReaderKitTests/RectMathTests.swift`

**Interfaces:**
- Consumes: nothing.
- Produces: `RectMath.IPoint {x, y: Int}` — `static func normalize(_ a: IPoint, _ b: IPoint) -> (x: Int, y: Int, w: Int, h: Int)` (drag corners in any order) and `static func toScreenAbsolute(viewX:viewY:screenMinX:screenMinY:screenHeight:globalTop:) -> (x: Int, y: Int)` (flipped overlay view → Quartz top-left space). Task 15 consumes them.

- [ ] **Step 1: Write the failing tests**

Create `app/Tests/ScreenReaderKitTests/RectMathTests.swift`:

```swift
import XCTest
@testable import ScreenReaderKit

final class RectMathTests: XCTestCase {
    func testNormalizeBothDragDirections() {
        let a = RectMath.IPoint(x: 100, y: 50), b = RectMath.IPoint(x: 200, y: 150)
        XCTAssertEqual(RectMath.normalize(a, b).x, 100)
        XCTAssertEqual(RectMath.normalize(a, b).y, 50)
        XCTAssertEqual(RectMath.normalize(a, b).w, 100)
        XCTAssertEqual(RectMath.normalize(a, b).h, 100)
        // reversed drag → same rect (T8 finding: tuples can't be XCTAssertEqual'd — not
        // Equatable-conforming; bare == is the compiler special case)
        XCTAssert(RectMath.normalize(b, a) == RectMath.normalize(a, b))
        // zero-size drag collapses
        XCTAssertEqual(RectMath.normalize(a, a).w, 0)
    }

    func testToScreenAbsolute_mainDisplay() {
        // main display: AppKit frame (0,0,1440,900) ⇒ globalTop = 900
        func map(_ vx: Int, _ vy: Int) -> (x: Int, y: Int) {
            RectMath.toScreenAbsolute(viewX: vx, viewY: vy,
                                       screenMinX: 0, screenMinY: 0, screenHeight: 900, globalTop: 900)
        }
        XCTAssertEqual(map(0, 0).x, 0);        XCTAssertEqual(map(0, 0).y, 0)   // top-left
        XCTAssertEqual(map(100, 40).x, 100);   XCTAssertEqual(map(100, 40).y, 40)
        XCTAssertEqual(map(1439, 899).x, 1439); XCTAssertEqual(map(1439, 899).y, 899) // bottom-right
    }

    func testToScreenAbsolute_secondaryBelowMain() {
        // secondary screen right + below main: AppKit frame (1440, -1080, 1920, 1080), globalTop = 900
        let r = RectMath.toScreenAbsolute(viewX: 0, viewY: 0,
                                          screenMinX: 1440, screenMinY: -1080, screenHeight: 1080, globalTop: 900)
        // view top-left = AppKit y = -1080+1080 = 0 ⇒ Quartz y = 900 - 0 = 900
        XCTAssertEqual(r.x, 1440); XCTAssertEqual(r.y, 900)
    }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter RectMathTests`
Expected: compile error — cannot find `RectMath`.

- [ ] **Step 3: Implement**

Create `app/Sources/ScreenReaderKit/RectMath.swift`:

```swift
import Foundation

public enum RectMath {
    public struct IPoint: Equatable {
        public var x: Int
        public var y: Int
        public init(x: Int, y: Int) { self.x = x; self.y = y }
    }

    /// Two drag corners (any order) → (x, y, w, h) with positive size.
    public static func normalize(_ a: IPoint, _ b: IPoint) -> (x: Int, y: Int, w: Int, h: Int) {
        (x: min(a.x, b.x), y: min(a.y, b.y), w: abs(b.x - a.x), h: abs(b.y - a.y))
    }

    /// Maps a point from a FLIPPED full-screen overlay view (origin top-left of
    /// that screen, Y down) to screen-absolute top-left-origin (Quartz) coords
    /// — the space the Go engine uses.
    ///   appkitY = screenMinY + screenHeight - viewY   (flipped → AppKit global)
    ///   quartzY = globalTop - appkitY                 (globalTop = max frame.maxY)
    public static func toScreenAbsolute(viewX: Int, viewY: Int,
                                        screenMinX: CGFloat, screenMinY: CGFloat,
                                        screenHeight: CGFloat, globalTop: CGFloat) -> (x: Int, y: Int) {
        let appkitX = screenMinX + CGFloat(viewX)
        let appkitY = screenMinY + screenHeight - CGFloat(viewY)
        return (x: Int(appkitX.rounded()), y: Int((globalTop - appkitY).rounded()))
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift test --filter RectMathTests`
Expected: all PASS. (If a secondary-display expectation fails, recheck the two-step flip in the comment — the main-display cases are the ones the app uses on a single-screen dev box.)

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/RectMath.swift app/Tests/ScreenReaderKitTests/RectMathTests.swift
git commit -m "feat(app): RectMath — drag normalization + flipped-view to Quartz mapping"
```

---

### Task 9: Swift Kit — Snippet + WindowLine

**Files:**
- Create: `app/Sources/ScreenReaderKit/Snippet.swift`
- Create: `app/Sources/ScreenReaderKit/WindowLine.swift`
- Test: `app/Tests/ScreenReaderKitTests/SnippetWindowLineTests.swift`

**Interfaces:**
- Consumes: nothing.
- Produces: `Snippet.opencodeEntry(addr: String, token: String) -> String` (Task 16 Server section UI text); `WindowEntry {app, title: String; x, y, w, h: Int}` + `WindowLine.parse(_ line: String) -> WindowEntry?` parsing the `list_windows` tool output format `app | title | {X:0 Y:0 W:0 H:0}` (Task 16 consumes it).

- [ ] **Step 1: Write the failing tests**

Create `app/Tests/ScreenReaderKitTests/SnippetWindowLineTests.swift`:

```swift
import XCTest
@testable import ScreenReaderKit

final class SnippetWindowLineTests: XCTestCase {
    func testSnippetWithToken() throws {
        let s = Snippet.opencodeEntry(addr: "127.0.0.1:18427", token: "abc")
        XCTAssertTrue(s.contains("http://127.0.0.1:18427/mcp"))
        XCTAssertTrue(s.contains("Bearer abc"))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any])
        let mcp = try XCTUnwrap(obj["mcp"] as? [String: Any])
        let servers = try XCTUnwrap(mcp["servers"] as? [String: Any])
        let sr = try XCTUnwrap(servers["screenreader"] as? [String: Any])
        XCTAssertEqual(sr["type"] as? String, "remote")
        XCTAssertEqual(sr["url"] as? String, "http://127.0.0.1:18427/mcp")
        XCTAssertEqual(sr["oauth"] as? Bool, false)
        let headers = try XCTUnwrap(sr["headers"] as? [String: Any])
        XCTAssertEqual(headers["Authorization"] as? String, "Bearer abc")
    }

    func testSnippetWithoutTokenOmitsHeaders() throws {
        let s = Snippet.opencodeEntry(addr: "127.0.0.1:1", token: "")
        XCTAssertFalse(s.contains("Bearer"))
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: Data(s.utf8)) as? [String: Any])
        let servers = try XCTUnwrap((obj["mcp"] as? [String: Any])?["servers"] as? [String: Any])
        let sr = try XCTUnwrap(servers["screenreader"] as? [String: Any])
        XCTAssertNil(sr["headers"])
    }

    func testWindowLineParse() {
        let w = WindowLine.parse("Microsoft Remote Desktop | 10.0.0.5 | {X:100 Y:50 W:800 H:600}")
        XCTAssertNotNil(w)
        XCTAssertEqual(w?.app, "Microsoft Remote Desktop")
        XCTAssertEqual(w?.title, "10.0.0.5")
        XCTAssertEqual(w?.x, 100); XCTAssertEqual(w?.y, 50)
        XCTAssertEqual(w?.w, 800); XCTAssertEqual(w?.h, 600)
    }

    func testWindowLineTitleWithPipes() {
        // app is the first field, bounds the last; a title containing " | " survives
        let w = WindowLine.parse("Finder | a | b | {X:0 Y:0 W:1 H:1}")
        XCTAssertEqual(w?.app, "Finder")
        XCTAssertEqual(w?.title, "a | b")
    }

    func testWindowLineRejectsBadInput() {
        XCTAssertNil(WindowLine.parse("no delimiters here"))
        XCTAssertNil(WindowLine.parse("app | title | notabounds"))
        XCTAssertNil(WindowLine.parse(""))
    }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter SnippetWindowLineTests`
Expected: compile error — cannot find `Snippet` / `WindowLine`.

- [ ] **Step 3: Implement**

Create `app/Sources/ScreenReaderKit/Snippet.swift`:

```swift
import Foundation

public enum Snippet {
    private struct Server: Encodable {
        let type: String
        let url: String
        let oauth: Bool
        let headers: [String: String]?
    }
    private struct Entry: Encodable {
        struct MCP: Encodable { let servers: [String: Server] }
        let mcp: MCP
    }

    /// The opencode.json server entry (a fragment to merge under mcp.servers)
    /// for this loopback endpoint.
    public static func opencodeEntry(addr: String, token: String) -> String {
        let entry = Entry(mcp: .init(servers: ["screenreader": .init(
            type: "remote",
            url: "http://\(addr)/mcp",
            oauth: false,
            headers: token.isEmpty ? nil : ["Authorization": "Bearer \(token)"],
        )]))
        let enc = JSONEncoder()
        // T9 finding: JSONSerialization escapes "/" (http:\/\/…), failing the
        // unescaped-URL assertion; JSONEncoder .withoutEscapingSlashes keeps it readable.
        enc.outputFormatting = [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes]
        guard let data = try? enc.encode(entry),
              let s = String(data: data, encoding: .utf8) else { return "{}" }
        return s
    }
}
```

Create `app/Sources/ScreenReaderKit/WindowLine.swift`:

```swift
import Foundation

public struct WindowEntry: Equatable {
    public var app: String
    public var title: String
    public var x: Int
    public var y: Int
    public var w: Int
    public var h: Int
    public init(app: String, title: String, x: Int, y: Int, w: Int, h: Int) {
        self.app = app; self.title = title
        self.x = x; self.y = y; self.w = w; self.h = h
    }
}

public enum WindowLine {
    /// Parses one `list_windows` result line:
    ///   app | title | {X:.. Y:.. W:.. H:..}
    /// The app is the first field and the bounds the last; a title containing
    /// " | " is preserved by rejoining the middle fields.
    public static func parse(_ line: String) -> WindowEntry? {
        let parts = line.components(separatedBy: " | ")
        guard parts.count >= 3 else { return nil }
        let app = parts[0].trimmingCharacters(in: .whitespaces)
        let title = parts[1..<parts.count - 1].joined(separator: " | ")
            .trimmingCharacters(in: .whitespaces)
        let bounds = parts.last!.trimmingCharacters(in: CharacterSet(charactersIn: "{} "))
        var vals: [String: Int] = [:]
        for kv in bounds.split(separator: " ") {
            let p = kv.split(separator: ":")
            guard p.count == 2, let v = Int(p[1]) else { return nil }
            vals[String(p[0])] = v
        }
        guard !app.isEmpty,
              let x = vals["X"], let y = vals["Y"], let w = vals["W"], let h = vals["H"]
        else { return nil }
        return WindowEntry(app: app, title: title, x: x, y: y, w: w, h: h)
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift test --filter SnippetWindowLineTests`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/ app/Tests/
git commit -m "feat(app): Snippet + WindowLine parsers"
```

---

### Task 10: Swift Kit — Backoff + CaptureCheck

**Files:**
- Create: `app/Sources/ScreenReaderKit/Backoff.swift`
- Create: `app/Sources/ScreenReaderKit/CaptureCheck.swift`
- Test: `app/Tests/ScreenReaderKitTests/BackoffCaptureTests.swift`

**Interfaces:**
- Consumes: nothing (CaptureCheck uses CoreGraphics/ImageIO only).
- Produces: `Backoff.delay(attempt: Int) -> Double` (Task 13); `CaptureCheck.isAllBlack(_ pngData: Data, threshold: UInt8 = 8) -> Bool` (Task 14 Test capture).

- [ ] **Step 1: Write the failing tests**

Create `app/Tests/ScreenReaderKitTests/BackoffCaptureTests.swift`:

```swift
import AppKit // only to synthesize PNG fixtures (NSBitmapImageRep)
import XCTest
@testable import ScreenReaderKit

final class BackoffCaptureTests: XCTestCase {
    func testBackoffSequence() {
        XCTAssertEqual(Backoff.delay(attempt: 0), 1)
        XCTAssertEqual(Backoff.delay(attempt: 1), 2)
        XCTAssertEqual(Backoff.delay(attempt: 2), 4)
        XCTAssertEqual(Backoff.delay(attempt: 4), 16)
        XCTAssertEqual(Backoff.delay(attempt: 5), 30) // capped
        XCTAssertEqual(Backoff.delay(attempt: -1), 1) // clamped low
    }

    private func png(_ gray: CGFloat) -> Data {
        let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 4, pixelsHigh: 4,
                                   bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false,
                                   isPlanar: false, colorSpaceName: .deviceRGB,
                                   bytesPerRow: 0, bitsPerPixel: 0)!
        for y in 0..<4 { for x in 0..<4 {
            // T10 host finding: NSColor(white:) (Generic Gray) silently writes BLACK
            // via setColor here (colorspace "Unrecognized colorspace number -1");
            // deviceRGB is reliable.
            rep.setColor(NSColor(deviceRed: gray, green: gray, blue: gray, alpha: 1), atX: x, y: y)
        } }
        return rep.representation(using: .png, properties: [:])!
    }

    func testIsAllBlack() {
        XCTAssertTrue(CaptureCheck.isAllBlack(png(0)))
        XCTAssertFalse(CaptureCheck.isAllBlack(png(1)))
    }

    func testIsAllBlackMixed() {
        let black = NSColor(deviceRed: 0, green: 0, blue: 0, alpha: 1)
        let white = NSColor(deviceRed: 1, green: 1, blue: 1, alpha: 1) // see T10 host finding above
        let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 4, pixelsHigh: 4,
                                   bitsPerSample: 8, samplesPerPixel: 3, hasAlpha: false,
                                   isPlanar: false, colorSpaceName: .deviceRGB,
                                   bytesPerRow: 0, bitsPerPixel: 0)!
        for y in 0..<4 { for x in 0..<4 { rep.setColor(black, atX: x, y: y) } }
        rep.setColor(white, atX: 2, y: 2)
        XCTAssertFalse(CaptureCheck.isAllBlack(rep.representation(using: .png, properties: [:])!))
    }

    func testUndecodableIsNotAllBlack() {
        XCTAssertFalse(CaptureCheck.isAllBlack(Data("garbage".utf8)))
    }
}
```

Note: this test file lives in `ScreenReaderKitTests` and imports AppKit only to build fixtures — production Kit code stays AppKit-free.

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter BackoffCaptureTests`
Expected: compile error — cannot find `Backoff` / `CaptureCheck`.

- [ ] **Step 3: Implement**

Create `app/Sources/ScreenReaderKit/Backoff.swift`:

```swift
import Foundation

public enum Backoff {
    /// Restart delay (seconds) for the given 0-based crash attempt: 1, 2, 4, … capped at 30.
    public static func delay(attempt: Int) -> Double {
        min(pow(2.0, Double(max(0, attempt))), 30.0)
    }
}
```

Create `app/Sources/ScreenReaderKit/CaptureCheck.swift`:

```swift
import CoreGraphics
import Foundation
import ImageIO

public enum CaptureCheck {
    /// True when `pngData` decodes and EVERY pixel is near-black (all channels
    /// ≤ threshold). The TCC canary: a capture without an effective Screen
    /// Recording grant comes back fully black. Undecodable data is NOT
    /// all-black (that's a different failure).
    public static func isAllBlack(_ pngData: Data, threshold: UInt8 = 8) -> Bool {
        guard let src = CGImageSourceCreateWithData(pngData as CFData, nil),
              let img = CGImageSourceCreateImageAtIndex(src, 0, nil),
              img.width > 0, img.height > 0 else { return false }
        // T10 finding: `CGContext(data: &bytes, …)` aliases an unreliable buffer
        // (the context writes to a different storage than the Swift array reads), so
        // use an explicitly allocated buffer we own, draw into it, then copy out.
        let count = img.width * img.height * 4
        let buf = UnsafeMutableRawPointer.allocate(byteCount: count, alignment: 16)
        defer { buf.deallocate() }
        guard let ctx = CGContext(data: buf, width: img.width, height: img.height,
                                  bitsPerComponent: 8, bytesPerRow: img.width * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        else { return false }
        ctx.draw(img, in: CGRect(x: 0, y: 0, width: img.width, height: img.height))
        let bytes = Array(UnsafeBufferPointer(start: buf.assumingMemoryBound(to: UInt8.self), count: count))
        for i in stride(from: 0, to: bytes.count, by: 4) {
            if bytes[i] > threshold || bytes[i + 1] > threshold || bytes[i + 2] > threshold {
                return false
            }
        }
        return true
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift test --filter BackoffCaptureTests`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/ app/Tests/
git commit -m "feat(app): server-restart backoff + all-black-capture detector"
```

---

### Task 11: Swift Kit — Permissions (TCC preflight + request)

**Files:**
- Create: `app/Sources/ScreenReaderKit/Permissions.swift`

**Interfaces:**
- Consumes: nothing.
- Produces: `TCCPermission` enum (`allCases` order: `.screenRecording, .accessibility, .automation`), `.isGranted: Bool`, `.request()`. Tasks 14/16 consume these exact names.

- [ ] **Step 1: Implement**

Create `app/Sources/ScreenReaderKit/Permissions.swift`:

```swift
import ApplicationServices
import CoreGraphics
import Foundation

public enum TCCPermission: String, CaseIterable {
    case screenRecording = "Screen Recording"
    case accessibility = "Accessibility"
    case automation = "Automation (System Events)"

    public var isGranted: Bool {
        switch self {
        case .screenRecording: return CGPreflightScreenCaptureAccess()
        case .accessibility:   return AXIsProcessTrusted()
        case .automation:      return Self.probeAutomation()
        }
    }

    /// Triggers the system prompt for this permission when not yet granted.
    public func request() {
        switch self {
        case .screenRecording:
            _ = CGRequestScreenCaptureAccess()
        case .accessibility:
            let opts = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
            _ = AXIsProcessTrustedWithOptions(opts)
        case .automation:
            _ = Self.probeAutomation() // running it is what surfaces the prompt
        }
    }

    /// Harmless System Events AppleScript — exit 0 only when Automation is granted.
    /// T11 finding: `to return 1` (literal) sends NO Apple event, so TCC is never
    /// exercised; `to get name` (a real property query) does. Matches the Go
    /// precedent in server/internal/perms/perms.go.
    static func probeAutomation() -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/osascript")
        p.arguments = ["-e", "tell application \"System Events\" to get name"]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        do {
            try p.run()
            p.waitUntilExit()
            return p.terminationStatus == 0
        } catch {
            return false
        }
    }
}
```

- [ ] **Step 2: Build**

Run: `cd app && swift build`
Expected: Build complete. (No new unit test — live TCC state can't be unit-tested; Tasks 14 and 17 verify it manually.)

- [ ] **Step 3: Commit**

```bash
git add app/Sources/ScreenReaderKit/Permissions.swift
git commit -m "feat(app): TCC permission preflight + request"
```

---

### Task 12: Swift Kit — MCPClient (with URLProtocol-stub tests)

**Files:**
- Create: `app/Sources/ScreenReaderKit/MCPClient.swift`
- Test: `app/Tests/ScreenReaderKitTests/MCPClientTests.swift`

**Interfaces:**
- Consumes: Foundation only.
- Produces: `MCPClientError: Error` with `.unauthorized`, `.server(Int)`, `.transport(String)`; `MCPClient` with `init(baseURL: URL, token: String? = nil, session: URLSession = …)`, `func callTool(_ name: String, arguments: [String: Any] = [:]) async throws -> [String: Any]` (returns the full `result` object), `func callToolText(_ name: String, arguments: [String: Any] = [:]) async throws -> String` (joined text contents). Performs `initialize` → `notifications/initialized` lazily once, carries `Mcp-Session-Id`, bearer header, tolerates JSON and `data:`-framed SSE bodies. Tasks 14/16 consume these names.

- [ ] **Step 1: Write the failing tests**

Create `app/Tests/ScreenReaderKitTests/MCPClientTests.swift`:

```swift
import Foundation
import XCTest
@testable import ScreenReaderKit

private final class StubURLProtocol: URLProtocol {
    nonisolated(unsafe) static var handler: ((URLRequest) -> (Int, [String: String], Data))?
    nonisolated(unsafe) static var requests: [URLRequest] = []

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        var req = request
        if req.httpBody == nil, let stream = req.httpBodyStream {
            // T12 finding: URLSession may move the body into a stream before
            // URLProtocol sees it, so reconstruct httpBody from httpBodyStream.
            stream.open()
            var data = Data()
            let bufferSize = 4096
            let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufferSize)
            while stream.hasBytesAvailable {
                let read = stream.read(buffer, maxLength: bufferSize)
                if read <= 0 { break }
                data.append(buffer, count: read)
            }
            buffer.deallocate()
            stream.close()
            req.httpBody = data
        }
        Self.requests.append(req)
        let (status, headers, body) = Self.handler!(req)
        var h = headers
        if h["Content-Type"] == nil { h["Content-Type"] = "application/json" }
        let resp = HTTPURLResponse(url: request.url!, statusCode: status,
                                   httpVersion: "HTTP/1.1", headerFields: h)!
        client?.urlProtocol(self, didReceive: resp, cacheStoragePolicy: .notAllowed)
        if !body.isEmpty { client?.urlProtocol(self, didLoad: body) }
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

final class MCPClientTests: XCTestCase {
    var session: URLSession!

    override func setUp() {
        StubURLProtocol.requests = []
        let cfg = URLSessionConfiguration.ephemeral
        cfg.protocolClasses = [StubURLProtocol.self]
        session = URLSession(configuration: cfg)
    }

    func testInitializeThenCallToolText() async throws {
        StubURLProtocol.handler = { req in
            let body = req.httpBody.flatMap { String(data: $0, encoding: .utf8) } ?? "" // T12: map yields String??
            if body.contains("\"initialize\"") {
                return (200, ["mcp-session-id": "S1"],
                        Data(#"{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"screenreader","version":"0.1.0"}}}"#.utf8))
            }
            if body.contains("notifications/initialized") {
                return (202, [:], Data())
            }
            if body.contains("\"tools/call\"") {
                return (200, [:],
                        Data(#"{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello from server"}]}}"#.utf8))
            }
            return (500, [:], Data(#"{"error":"unexpected"}"#.utf8))
        }

        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!,
                           token: "tok", session: session)
        let text = try await c.callToolText("list_windows")
        XCTAssertEqual(text, "hello from server")

        // session + auth headers carried on the tools/call
        let call = try XCTUnwrap(StubURLProtocol.requests.last)
        XCTAssertEqual(call.value(forHTTPHeaderField: "Mcp-Session-Id"), "S1")
        XCTAssertEqual(call.value(forHTTPHeaderField: "Authorization"), "Bearer tok")
    }

    func testSSEResponseIsParsed() async throws {
        StubURLProtocol.handler = { req in
            let body = req.httpBody.flatMap { String(data: $0, encoding: .utf8) } ?? "" // T12: map yields String??
            if body.contains("\"initialize\"") {
                return (200, ["mcp-session-id": "S1"],
                        Data(#"{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"screenreader","version":"0.1.0"}}}"#.utf8))
            }
            if body.contains("notifications/initialized") { return (202, [:], Data()) }
            return (200, ["Content-Type": "text/event-stream"],
                    Data("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"sse ok\"}]}}\n\n".utf8))
        }
        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!, session: session)
        let text = try await c.callToolText("doctor")
        XCTAssertEqual(text, "sse ok")
    }

    func testUnauthorizedThrows() async {
        StubURLProtocol.handler = { _ in (401, [:], Data("nope".utf8)) }
        let c = MCPClient(baseURL: URL(string: "http://127.0.0.1:18427")!, session: session)
        do {
            _ = try await c.callToolText("capture")
            XCTFail("expected unauthorized")
        } catch let e as MCPClientError {
            guard case .unauthorized = e else { return XCTFail("wrong error: \(e)") }
        } catch {
            XCTFail("wrong error type: \(error)")
        }
    }
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd app && swift test --filter MCPClientTests`
Expected: compile error — cannot find `MCPClient`.

- [ ] **Step 3: Implement**

Create `app/Sources/ScreenReaderKit/MCPClient.swift`:

```swift
import Foundation

public enum MCPClientError: Error {
    case unauthorized
    case server(Int)
    case transport(String)
}

/// Minimal MCP client over Streamable HTTP: initialize handshake + tools/call.
/// The server is configured for JSON responses; SSE `data:` frames are tolerated.
public final class MCPClient: @unchecked Sendable {
    private let baseURL: URL
    private let token: String?
    private let session: URLSession
    private let lock = NSLock()
    private var sessionId: String?
    private var ready = false

    public init(baseURL: URL, token: String? = nil,
                session: URLSession = URLSession(configuration: .ephemeral)) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    /// Calls a tool and returns the full `result` JSON object.
    public func callTool(_ name: String, arguments: [String: Any] = [:]) async throws -> [String: Any] {
        try await ensureReady()
        var params: [String: Any] = ["name": name]
        if !arguments.isEmpty { params["arguments"] = arguments }
        let msg = try await send(id: 3, method: "tools/call", params: params)
        if let err = msg["error"] as? [String: Any] {
            throw MCPClientError.transport("\(err["message"] ?? "tool error")")
        }
        guard let result = msg["result"] as? [String: Any] else {
            throw MCPClientError.transport("no result in response")
        }
        return result
    }

    /// Calls a tool and returns its text content joined.
    public func callToolText(_ name: String, arguments: [String: Any] = [:]) async throws -> String {
        let result = try await callTool(name, arguments: arguments)
        let content = result["content"] as? [[String: Any]] ?? []
        let text = content.compactMap { item in
            (item["type"] as? String) == "text" ? (item["text"] as? String) : nil
        }.joined(separator: "\n")
        return text
    }

    private func ensureReady() async throws {
        lock.lock()
        let was = ready
        lock.unlock()
        guard !was else { return }
        _ = try await send(id: 1, method: "initialize", params: [
            "protocolVersion": "2025-06-18",
            "capabilities": [String: Any](),
            "clientInfo": ["name": "screenreader-app", "version": "0.1.0"],
        ])
        try await send(method: "notifications/initialized", params: [String: Any]())
        lock.lock()
        ready = true
        lock.unlock()
    }

    @discardableResult
    private func send(id: Int? = nil, method: String, params: [String: Any]) async throws -> [String: Any] {
        var body: [String: Any] = ["jsonrpc": "2.0", "method": method, "params": params]
        if let id { body["id"] = id }
        // T12 findings: declaration reordered (call sites pass id: first — Swift arg
        // order); withoutEscapingSlashes (default emits tools\/call, breaking the
        // stub's unescaped method-string match; wire semantics identical).
        guard let data = try? JSONSerialization.data(withJSONObject: body,
                                                      options: [.withoutEscapingSlashes]) else {
            throw MCPClientError.transport("encode failed")
        }
        var req = URLRequest(url: baseURL.appendingPathComponent("mcp"))
        req.httpMethod = "POST"
        req.timeoutInterval = 10
        req.httpBody = data
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("application/json, text/event-stream", forHTTPHeaderField: "Accept")
        if let token { req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization") }
        lock.lock()
        if let sid = sessionId { req.setValue(sid, forHTTPHeaderField: "Mcp-Session-Id") }
        lock.unlock()

        let (raw, resp): (Data, URLResponse)
        do { (raw, resp) = try await session.data(for: req) }
        catch { throw MCPClientError.transport(error.localizedDescription) }
        guard let http = resp as? HTTPURLResponse else {
            throw MCPClientError.transport("no HTTP response")
        }
        switch http.statusCode {
        case 200, 202: break
        case 401: throw MCPClientError.unauthorized
        default: throw MCPClientError.server(http.statusCode)
        }
        if let sid = http.value(forHTTPHeaderField: "Mcp-Session-Id") ??
            http.value(forHTTPHeaderField: "mcp-session-id") {
            lock.lock(); sessionId = sid; lock.unlock()
        }
        guard !raw.isEmpty else { return [:] }
        // JSON body, or SSE frames — take the last data: line that carries an id.
        let ct = http.value(forHTTPHeaderField: "Content-Type") ?? ""
        var payload = raw
        if ct.contains("text/event-stream") {
            let s = String(decoding: raw, as: UTF8.self)
            for line in s.components(separatedBy: "\n") where line.hasPrefix("data:") {
                let json = line.dropFirst(5).trimmingCharacters(in: .whitespaces)
                if json.contains("\"id\"") { payload = Data(json.utf8) }
            }
        }
        guard let obj = try? JSONSerialization.jsonObject(with: payload),
              let dict = obj as? [String: Any] else {
            throw MCPClientError.transport("unparseable response")
        }
        return dict
    }
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd app && swift test --filter MCPClientTests`
Expected: all PASS. Then the whole suite: `swift test` — all green.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReaderKit/MCPClient.swift app/Tests/ScreenReaderKitTests/MCPClientTests.swift
git commit -m "feat(app): minimal MCP-over-HTTP client (initialize + tools/call)"
```

---

### Task 13: App — ServerProcess (spawn / supervise / stop)

**Files:**
- Create: `app/Sources/ScreenReader/ServerProcess.swift`
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (start/stop + token bootstrap + Restart item)

**Interfaces:**
- Consumes: `ConfigStore.load/httpConfig/ensureToken/generateToken/save`, `Backoff.delay(attempt:)`, `Bundle.main.resourceURL`.
- Produces: `ServerProcess` with `enum State {stopped, starting, running, restarting, crashed}`, `var state: State` (main-thread), `var onStateChange: ((State) -> Void)?`, `func start()`, `func stop()`, `func restart()` (≡ start: kill-if-any, respawn clean), `var stderrText: String` (tail). Tasks 14/16/17 consume these names. Threading contract: `proc`/`attempts`/`errTail`/`stopping` are touched **only on the private queue `q`**; `state`/`onStateChange` only on main.

- [ ] **Step 1: Implement ServerProcess**

Create `app/Sources/ScreenReader/ServerProcess.swift`:

```swift
import AppKit
import Foundation
import ScreenReaderKit

/// T13 fix: child pid for the SIGTERM handler. Written on q in spawn()/killChild().
/// SIGTERM (killall / force-quit) never runs applicationWillTerminate, so the
/// handler kills the child directly. The Go server does not trap SIGTERM.
/// T13 fix-round note: UnsafeAtomic lives in Synchronization (macOS 15+); the
/// package targets macOS 13, so a plain aligned Int32 is used — a 32-bit
/// aligned load/store is lock-free on arm64/x86_64, and global literal init
/// is static, so the handler never races lazy initialization.
private var sigChildPid: Int32 = 0

/// Spawns and supervises the embedded Go server (`screenreader --http`):
/// SIGTERM on quit, auto-restart with backoff after crashes, /health polling
/// for state. All mutable process bookkeeping lives on one serial queue.
final class ServerProcess {
    enum State: Equatable { case stopped, starting, running, restarting, crashed }

    var onStateChange: ((State) -> Void)?
    private(set) var state: State = .stopped

    var binaryURL: URL? { Bundle.main.resourceURL?.appendingPathComponent("screenreader") }

    private var proc: Process?
    private var errTail = ""
    private var attempts = 0
    private var stopping = false
    private var healthTimer: Timer?
    private let q = DispatchQueue(label: "screenreader.server")

    init() { signal(SIGTERM, handleSigterm) }

    private var addr: String {
        ((try? ConfigStore.load()).map { ConfigStore.httpConfig(from: $0).addr })
            ?? "127.0.0.1:18427"
    }

    var stderrText: String { q.sync { errTail } }

    /// Clean (re)launch: kills any lingering child and spawns fresh.
    func start() {
        q.async {
            self.stopping = false
            self.attempts = 0
            self.killStaleListener() // T13 fix: sweep port orphans (SIGKILL etc.)
            self.killChild()
            self.spawn()
        }
    }

    /// T13 fix: kill any leftover process still LISTENing on our port (SIGKILL
    /// orphans, prior crashes) so the new child can bind. No-op when free.
    private func killStaleListener() {
        guard let colon = addr.lastIndex(of: ":") else { return }
        let port = String(addr[addr.index(after: colon)...])
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/sbin/lsof")
        p.arguments = ["-ti", "tcp:\(port)", "-sTCP:LISTEN"]
        let pipe = Pipe()
        p.standardOutput = pipe
        p.standardError = FileHandle.nullDevice
        do {
            try p.run()
            p.waitUntilExit()
            let out = String(decoding: pipe.fileHandleForReading.readDataToEndOfFile(), as: UTF8.self)
            for line in out.split(whereSeparator: \.isNewline) {
                if let pid = Int32(line.trimmingCharacters(in: .whitespaces)) { kill(pid, SIGTERM) }
            }
        } catch {}
    }

    func restart() { start() }

    func stop() {
        DispatchQueue.main.async { self.healthTimer?.invalidate(); self.healthTimer = nil }
        q.async {
            self.stopping = true
            self.killChild()
            self.set(.stopped)
        }
    }

    private func spawn() {
        guard let bin = binaryURL, FileManager.default.isExecutableFile(atPath: bin.path) else {
            errTail = "embedded Go binary not found — run build.sh"
            set(.crashed)
            return
        }
        set(attempts == 0 ? .starting : .restarting)
        let p = Process()
        p.executableURL = bin
        p.arguments = ["--http"]
        p.standardOutput = FileHandle.nullDevice
        let pipe = Pipe()
        p.standardError = pipe
        pipe.fileHandleForReading.readabilityHandler = { [weak self] h in
            let d = h.availableData
            guard let self, !d.isEmpty else { return }
            self.q.async {
                self.errTail += String(decoding: d, as: UTF8.self)
                if self.errTail.count > 4000 { self.errTail = String(self.errTail.suffix(4000)) }
            }
        }
        p.terminationHandler = { [weak self] done in
            self?.q.async {
                // Ignore exits of replaced children (restart/stop) — only the
                // CURRENT child crashing unexpectedly triggers the backoff.
                guard let self, !self.stopping, self.proc === done else { return }
                self.proc = nil
                self.handleExit()
            }
        }
        do {
            try p.run()
            proc = p
            sigChildPid = p.processIdentifier // T13 fix
            startHealth()
        } catch {
            errTail = "spawn failed: \(error.localizedDescription)"
            set(.crashed)
        }
    }

    private func handleExit() {
        guard attempts < 3 else { set(.crashed); return }
        let d = Backoff.delay(attempt: attempts)
        attempts += 1
        set(.restarting)
        q.asyncAfter(deadline: .now() + d) { [weak self] in
            guard let self, !self.stopping else { return }
            self.spawn()
        }
    }

    /// Must run on q. Detaches the child FIRST so its termination handler is
    /// recognized as stale and does not trigger a backoff restart.
    private func killChild() {
        guard let p = proc else { return }
        proc = nil
        sigChildPid = 0 // T13 fix
        if p.isRunning {
            p.terminate() // SIGTERM
            let deadline = Date().addingTimeInterval(3)
            while p.isRunning && Date() < deadline { usleep(50_000) }
            if p.isRunning { kill(p.processIdentifier, SIGKILL) }
        }
    }

    private func startHealth() {
        DispatchQueue.main.async { [weak self] in
            guard let self else { return }
            self.healthTimer?.invalidate()
            let t = Timer(timeInterval: 2, repeats: true) { [weak self] _ in self?.checkHealth() }
            RunLoop.main.add(t, forMode: .common)
            self.healthTimer = t
            self.checkHealth()
        }
    }

    private func checkHealth() {
        guard let url = URL(string: "http://\(addr)/health") else { return }
        var req = URLRequest(url: url)
        req.timeoutInterval = 2
        URLSession.shared.dataTask(with: req) { [weak self] data, resp, _ in
            guard let self else { return }
            let ok = (resp as? HTTPURLResponse)?.statusCode == 200 && data != nil
            if ok { self.q.async { self.attempts = 0; self.set(.running) } }
        }.resume()
    }

    private func set(_ s: State) {
        DispatchQueue.main.async {
            self.state = s
            self.onStateChange?(s)
        }
    }
}

/// T13 fix: SIGTERM (killall / force-quit) does not run applicationWillTerminate.
/// No captures → converts to a C function pointer; body is async-signal-safe
/// (lock-free atomic load + kill(2) + _exit(3)).
private func handleSigterm(_ sig: Int32) {
    let pid = sigChildPid
    if pid > 0 { kill(pid, SIGTERM) }
    _exit(143)
}
```

- [ ] **Step 2: Wire into AppDelegate**

Replace `app/Sources/ScreenReader/AppDelegate.swift` with:

```swift
import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()

    func applicationDidFinishLaunching(_ notification: Notification) {
        ensureToken()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem.button?.title = "SR"
        let menu = NSMenu()
        let restart = menu.addItem(withTitle: "Restart server",
                                   action: #selector(restartServer), keyEquivalent: "")
        restart.target = self
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        statusItem.menu = menu
        server.start()
    }

    func applicationWillTerminate(_ notification: Notification) { server.stop() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    @objc private func restartServer() { server.restart() }

    /// First run: give the config an http section with a bearer token.
    /// A malformed config is left untouched (the server logs its own error).
    private func ensureToken() {
        do {
            var json = try ConfigStore.load()
            if ConfigStore.httpConfig(from: json).token.isEmpty {
                json = ConfigStore.ensureToken(ConfigStore.generateToken(), in: json)
                try ConfigStore.save(json)
            }
        } catch {
            NSLog("screenreader: config.json unreadable, leaving it alone: \(error)")
        }
    }
}
```

- [ ] **Step 3: Build + run test target**

Run: `cd app && swift build && swift test`
Expected: build OK; all Kit tests pass.

- [ ] **Step 4: Manual lifecycle verification**

```bash
./build.sh && open app/build/ScreenReader.app
sleep 2
pgrep -fl "screenreader --http"          # expect: a child process
curl -s http://127.0.0.1:18427/health    # expect: {"ok":true,…}
# kill the child → auto-restarts within ~1s:
kill $(pgrep -f "screenreader --http")
sleep 2
curl -s http://127.0.0.1:18427/health    # expect: ok again
# quit the app (menu → Quit) → child gone, no backoff restart:
killall ScreenReader 2>/dev/null; sleep 1
pgrep -f "screenreader --http" || echo "child gone (expected)"
```

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReader/
git commit -m "feat(app): server process supervision — spawn/backoff/health/stop"
```

---

### Task 14: App — menu-bar status + Test capture (TCC CANARY GATE)

**This task contains spec §7's structural-risk gate.** Do not proceed to Tasks 15–18 until **Test capture** returns a non-black image. If it stays black after correct granting, stop and escalate to the user: the design falls back to spec §7 Option B (Swift-native low-level layer), which invalidates UI work — that's why the UI tasks come after this gate.

**Files:**
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (full replace)

**Interfaces:**
- Consumes: `ServerProcess.state/onStateChange`, `TCCPermission`, `ConfigStore`, `MCPClient.callTool`, `CaptureCheck`.
- Produces: the live status menu (spec §4.2, minus the Open item that Task 16 adds) and the **Test capture…** canary action. Task 16/17 reuse `rebuild()` and the alert helpers.

- [ ] **Step 1: Implement**

Replace `AppDelegate.swift` with:

```swift
import AppKit
import ScreenReaderKit

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var statusItem: NSStatusItem!
    let server = ServerProcess()

    func applicationDidFinishLaunching(_ notification: Notification) {
        ensureToken()
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let img = NSImage(systemSymbolName: "menubar.rectangle",
                             accessibilityDescription: "ScreenReader") {
            statusItem.button?.image = img
        } else {
            statusItem.button?.title = "SR"
        }
        let menu = NSMenu()
        menu.delegate = self
        statusItem.menu = menu
        rebuild()
        server.onStateChange = { [weak self] _ in self?.rebuild() }
        server.start()
    }

    func applicationWillTerminate(_ notification: Notification) { server.stop() }
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }
    func menuWillOpen(_ menu: NSMenu) { rebuild() }

    @objc private func restartServer() { server.restart() }

    // ensureToken(): keep the Task 13 version unchanged.

    /// Rebuild the status menu: live server/target/permission lines + actions.
    private func rebuild() {
        guard let menu = statusItem.menu else { return }
        menu.removeAllItems()
        let json = ((try? ConfigStore.load()) ?? [:])
        let (addr, _) = ConfigStore.httpConfig(from: json)

        func line(_ s: String) {
            let i = NSMenuItem(title: s, action: nil, keyEquivalent: "")
            i.isEnabled = false
            menu.addItem(i)
        }
        line("Server: " + {
            switch server.state {
            case .running: return "running on \(addr)"
            case .starting, .restarting: return "starting…"
            case .crashed: return "CRASHED — check the log"
            case .stopped: return "not running"
            }
        }())
        let t = ConfigStore.target(from: json)
        if t.mode == "rect", t.w > 0 {
            line("Target: rectangle (\(t.x), \(t.y), \(t.w), \(t.h))")
        } else if !t.app.isEmpty {
            line("Target: \(t.app)" + (t.title.isEmpty ? "" : " — \(t.title)"))
        } else {
            line("Target: not set")
        }
        for p in TCCPermission.allCases {
            line("\(p.rawValue): " + (p.isGranted ? "✓" : "✗"))
        }
        menu.addItem(.separator())
        for (label, sel) in [("Restart server", #selector(restartServer)),
                             ("Test capture…", #selector(testCapture))] {
            let i = menu.addItem(withTitle: label, action: sel, keyEquivalent: "")
            i.target = self
        }
        menu.addItem(.separator())
        menu.addItem(withTitle: "Quit ScreenReader",
                     action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
    }

    /// The spec §7 canary: run a real capture through the Go child and check
    /// that its pixels are not uniformly black (= the app's Screen Recording
    /// grant is in effect for the child process).
    @objc private func testCapture() {
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        guard let url = URL(string: "http://\(addr)") else { return }
        let client = MCPClient(baseURL: url, token: token.isEmpty ? nil : token)
        Task { @MainActor in
            let result: [String: Any]
            do { result = try await client.callTool("capture") }
            catch { alert("Test capture failed", "\(error)"); return }

            if result["isError"] as? Bool == true {
                alert("Test capture failed", textContent(result) ?? "tool error")
                return
            }
            guard let content = result["content"] as? [[String: Any]],
                  let img = content.first(where: { $0["type"] as? String == "image" }),
                  let b64 = img["data"] as? String,
                  let png = Data(base64Encoded: b64) else {
                alert("Test capture failed", textContent(result) ?? "capture returned no image")
                return
            }
            if CaptureCheck.isAllBlack(png) {
                alert("Test capture: ALL BLACK",
                      "Screen Recording is not effective for the app. Check System Settings → " +
                      "Privacy & Security → Screen Recording shows ScreenReader ITSELF (not your " +
                      "terminal), then quit and reopen the app. If it is still black, STOP — the " +
                      "child-process attribution assumption (§7) failed and must be escalated.")
            } else {
                alert("Test capture OK",
                      "Non-black image — the Go server runs under the app's permissions.")
            }
        }
    }

    private func textContent(_ result: [String: Any]) -> String? {
        ((result["content"] as? [[String: Any]])?
            .compactMap { ($0["type"] as? String) == "text" ? ($0["text"] as? String) : nil })?
            .joined(separator: "\n")
    }

    private func alert(_ title: String, _ info: String) {
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        a.runModal()
    }
}
```

- [ ] **Step 2: Build + test**

Run: `cd app && swift build && swift test`
Expected: build OK, Kit tests green.

- [ ] **Step 3: RUN THE CANARY (manual, permission-gated) — hard gate**

```bash
./build.sh && open app/build/ScreenReader.app
# 1. Menu shows: Server line, Target line, three permission lines (Screen ✗ initially).
# 2. System Settings → Privacy & Security → Screen Recording → enable ScreenReader
#    → quit and reopen the app (grant note: only Screen Recording is needed for
#    this canary; Accessibility/Automation can stay ✗ for now).
# 3. Point a rect target at some non-black screen region, e.g.:
python3 - <<'PY'
import json, os
p = os.path.expanduser("~/.config/screenreader/config.json")
d = json.load(open(p))
d["mode"] = "rect"; d["rect"] = {"x": 0, "y": 0, "w": 600, "h": 400}
json.dump(d, open(p, "w"), indent=2)
PY
# 4. Menu → Test capture…
```

Expected: **"Test capture OK"** alert. Failure triage:
- Error alert "no target configured" → redo step 3 (config was overwritten).
- **ALL BLACK** → verify Screen Recording is granted to *ScreenReader* itself (not Terminal/opencode), quit+reopen, retest.
- Still ALL BLACK → **STOP THE WHOLE PLAN.** Report to the user: attribution failed, escalate to spec §7 Option B before any UI task (15+) proceeds.
- Also confirm `Automation`/`Accessibility` rows flip to ✓ after granting those (menu refreshes on open).

- [ ] **Step 4: Commit**

```bash
git add app/Sources/ScreenReader/AppDelegate.swift
git commit -m "feat(app): live menu-bar status + Test capture TCC canary"
```

---

### Task 15: App — RectOverlay (rectangle drawing)

**Files:**
- Create: `app/Sources/ScreenReader/RectOverlay.swift`
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (temporary DEV trigger, removed in Task 16)

**Interfaces:**
- Consumes: `RectMath.normalize`, `RectMath.toScreenAbsolute`.
- Produces: `RectOverlay` with `var onResult: ((Int, Int, Int, Int) -> Void)?` (screen-absolute top-left x, y, w, h) and `func present()`. Task 16 consumes these.

- [ ] **Step 1: Implement**

Create `app/Sources/ScreenReader/RectOverlay.swift`:

```swift
import AppKit
import ScreenReaderKit

/// Transparent full-screen overlay (one borderless window per display). Drag
/// to draw a rect; on release it is delivered in screen-absolute, top-left
/// origin, Y-down coords (the engine's space). Esc cancels silently; a
/// near-zero drag delivers nothing.
final class RectOverlay {
    var onResult: ((Int, Int, Int, Int) -> Void)?
    private var windows: [NSWindow] = []

    private final class OverlayWindow: NSWindow {
        override var canBecomeKey: Bool { true } // borderless windows can't be key by default
    }

    func present() {
        dismiss()
        let globalTop = NSScreen.screens.map { $0.frame.maxY }.max() ?? 0
        for screen in NSScreen.screens {
            let win = OverlayWindow(contentRect: screen.frame, styleMask: [.borderless],
                                    backing: .buffered, defer: false)
            win.level = .screenSaver
            win.backgroundColor = NSColor.black.withAlphaComponent(0.2)
            win.isOpaque = false
            win.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]
            let v = DragView(frame: NSRect(origin: .zero, size: screen.frame.size))
            let (minX, minY, height) = (screen.frame.minX, screen.frame.minY, screen.frame.height)
            v.onFinish = { [weak self] a, b in
                guard let self else { return }
                self.dismiss()
                let pa = RectMath.toScreenAbsolute(viewX: a.x, viewY: a.y,
                                                   screenMinX: minX, screenMinY: minY,
                                                   screenHeight: height, globalTop: globalTop)
                let pb = RectMath.toScreenAbsolute(viewX: b.x, viewY: b.y,
                                                   screenMinX: minX, screenMinY: minY,
                                                   screenHeight: height, globalTop: globalTop)
                let x = min(pa.x, pb.x), y = min(pa.y, pb.y)
                let w = abs(pb.x - pa.x), h = abs(pb.y - pa.y)
                if w >= 2 && h >= 2 { self.onResult?(x, y, w, h) }
            }
            win.contentView = v
            win.makeKeyAndOrderFront(nil)
            windows.append(win)
        }
        NSApp.activate(ignoringOtherApps: true)
    }

    private func dismiss() {
        for w in windows { w.orderOut(nil) }
        windows.removeAll()
    }

    /// Draws in its OWN flipped coordinates: origin top-left of this screen,
    /// Y down. Conversion to global space happens once, in present().
    private final class DragView: NSView {
        var onFinish: ((RectMath.IPoint, RectMath.IPoint) -> Void)?
        private var a: RectMath.IPoint?
        private var b: RectMath.IPoint?

        override var isFlipped: Bool { true }
        override var acceptsFirstResponder: Bool { true }

        override func mouseDown(with e: NSEvent) {
            let p = convert(e.locationInWindow, from: nil)
            a = RectMath.IPoint(x: Int(p.x), y: Int(p.y)); b = a
            needsDisplay = true
        }
        override func mouseDragged(with e: NSEvent) {
            guard a != nil else { return }
            let p = convert(e.locationInWindow, from: nil)
            b = RectMath.IPoint(x: Int(p.x), y: Int(p.y))
            needsDisplay = true
        }
        override func mouseUp(with e: NSEvent) {
            let (sa, sb) = (a, b); a = nil; b = nil
            if let sa, let sb { onFinish?(sa, sb) }
        }
        override func keyDown(with e: NSEvent) {
            if e.keyCode == 53 { onFinish = nil; window?.orderOut(nil) } // Esc cancels
        }
        override func draw(_ dirtyRect: NSRect) {
            guard let a, let b else { return }
            let n = RectMath.normalize(a, b)
            let path = NSBezierPath(rect: NSRect(x: n.x, y: n.y, width: n.w, height: n.h))
            NSColor.white.setStroke()
            path.lineWidth = 2
            path.stroke()
        }
    }
}
```

- [ ] **Step 2: Temporary DEV trigger in AppDelegate**

In `applicationDidFinishLaunching`, after the `Test capture…` loop (Task 14 code), add (temporary; Task 16 removes it):

```swift
        let draw = menu.addItem(withTitle: "DEV draw rect…",
                                action: #selector(devDraw), keyEquivalent: "")
        draw.target = self
```

And at the end of the class:

```swift
    // T15 fix: retain the overlay for the drag's lifetime — devDraw's local `o`
    // would otherwise deallocate on return (onFinish only holds it weakly), so
    // onResult would never fire and both windows would stay up.
    private var overlay: RectOverlay?

    @objc private func devDraw() {
        let o = RectOverlay()
        o.onResult = { x, y, w, h in NSLog("rect = %d, %d, %d, %d", x, y, w, h) }
        overlay = o
        o.present()
    }
```

- [ ] **Step 3: Build**

Run: `cd app && swift build && swift test`
Expected: build OK, tests green.

- [ ] **Step 4: Manual verification**

```bash
./build.sh && open app/build/ScreenReader.app
log stream --predicate 'process == "ScreenReader"' --level info > /tmp/sr-rect.log &
LOGPID=$!
# menu → "DEV draw rect…" → drag a clearly identifiable region (e.g. around a
# known window), release; then Escape-cancel a second drag (must log nothing).
sleep 2; kill $LOGPID
grep "rect =" /tmp/sr-rect.log
```

Expected: one `rect = x, y, w, h` line; sanity: positive w/h, origin at the top-left of the dragged area. Exact cross-check against the Go capture space happens in Task 17 step 5 — do NOT tune the math by eye here.

- [ ] **Step 5: Commit**

```bash
git add app/Sources/ScreenReader/
git commit -m "feat(app): full-screen rect-drawing overlay (flipped, per-display)"
```

---

### Task 16: App — SettingsWindow (Permissions / Target / Server)

**Files:**
- Create: `app/Sources/ScreenReader/SettingsWindow.swift`
- Modify: `app/Sources/ScreenReader/AppDelegate.swift` (remove DEV trigger, add Open item)

**Interfaces:**
- Consumes: `TCCPermission`, `ConfigStore`, `MCPClient.callToolText`, `WindowLine`, `RectOverlay`, `Snippet`, `ServerProcess` (all from Tasks 7–15).
- Produces: `SettingsWindowController(server:)` — one window with the three spec §4.3 sections; menu item **Open ScreenReader…**. Task 17's checklist drives it.

- [ ] **Step 1: Implement**

Create `app/Sources/ScreenReader/SettingsWindow.swift`:

```swift
import AppKit
import ScreenReaderKit

final class SettingsWindowController: NSWindowController, NSWindowDelegate {
    private let server: ServerProcess
    private var permStatus: [TCCPermission: NSTextField] = [:]
    private var grantButton: NSButton!
    private var segmented: NSSegmentedControl!
    private var pickerRow: NSStackView!
    private var rectRow: NSStackView!
    private var picker: NSPopUpButton!
    private var rectLabel: NSTextField!
    private var serverLabel: NSTextField!
    private var snippetView: NSTextView!
    private var stderrView: NSTextView!
    private var lastModeKey = ""
    private var timer: Timer?

    init(server: ServerProcess) {
        self.server = server
        let win = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 460, height: 620),
                           styleMask: [.titled, .closable], backing: .buffered, defer: false)
        win.title = "ScreenReader"
        win.center()
        super.init(window: win)
        win.delegate = self // T16 fix: stop the refresh timer on window close
        build()
        refresh()
        let t = Timer(timeInterval: 2, repeats: true) { [weak self] _ in self?.refresh() }
        RunLoop.main.add(t, forMode: .common)
        timer = t
    }

    required init?(coder: NSCoder) { fatalError("not used") }
    deinit { timer?.invalidate() }

    // T16 fix: the 2s timer must not outlive the window — otherwise the
    // .automation probe (osascript spawn + waitUntilExit) stalls the main
    // thread every 2s for the app's whole life (settingsWC was never nilled).
    func windowWillClose(_ notification: Notification) { timer?.invalidate() }

    // MARK: - Build

    private func build() {
        let root = stack(.vertical)
        root.spacing = 14
        root.alignment = .leading

        // --- Permissions ---
        let perms = stack(.vertical)
        perms.addArrangedSubview(sectionTitle("Permissions"))
        for (idx, p) in TCCPermission.allCases.enumerated() {
            let row = stack(.horizontal)
            let status = NSTextField(labelWithString: "…")
            let btn = NSButton(title: "Request", target: self, action: #selector(requestPerm(_:)))
            btn.tag = idx
            row.addArrangedSubview(NSTextField(labelWithString: p.rawValue))
            row.addArrangedSubview(NSView()) // spacer
            row.addArrangedSubview(status)
            row.addArrangedSubview(btn)
            row.widthAnchor.constraint(equalToConstant: 420).isActive = true
            permStatus[p] = status
            perms.addArrangedSubview(row)
        }
        grantButton = NSButton(title: "Grant access", target: self, action: #selector(grantAll))
        perms.addArrangedSubview(grantButton)
        root.addArrangedSubview(perms)

        // --- Target ---
        let target = stack(.vertical)
        target.addArrangedSubview(sectionTitle("Target"))
        segmented = NSSegmentedControl(labels: ["App window", "Rectangle"],
                                       trackingMode: .selectOne, target: self,
                                       action: #selector(modeChanged))
        target.addArrangedSubview(segmented)

        pickerRow = stack(.horizontal)
        picker = NSPopUpButton(frame: .zero, pullsDown: false)
        picker.target = self
        picker.action = #selector(pickerChanged)
        picker.widthAnchor.constraint(equalToConstant: 320).isActive = true
        pickerRow.addArrangedSubview(picker)
        pickerRow.addArrangedSubview(NSButton(title: "Refresh", target: self,
                                              action: #selector(refreshPicker)))

        rectRow = stack(.horizontal)
        rectRow.addArrangedSubview(NSButton(title: "Select rectangle…", target: self,
                                            action: #selector(selectRect)))
        rectLabel = NSTextField(labelWithString: "")
        rectLabel.font = NSFont.monospacedSystemFont(ofSize: 11, weight: .regular)
        rectRow.addArrangedSubview(rectLabel)

        target.addArrangedSubview(pickerRow)
        target.addArrangedSubview(rectRow)
        root.addArrangedSubview(target)

        // --- Server ---
        let srv = stack(.vertical)
        srv.addArrangedSubview(sectionTitle("Server"))
        let srvRow = stack(.horizontal)
        serverLabel = NSTextField(labelWithString: "…")
        srvRow.addArrangedSubview(serverLabel)
        srvRow.addArrangedSubview(NSView())
        srvRow.addArrangedSubview(NSButton(title: "Restart", target: self,
                                           action: #selector(restartServer)))
        srvRow.addArrangedSubview(NSButton(title: "Copy opencode config", target: self,
                                           action: #selector(copySnippet)))
        srvRow.widthAnchor.constraint(equalToConstant: 420).isActive = true
        srv.addArrangedSubview(srvRow)
        snippetView = readOnlyTextView(height: 110)
        srv.addArrangedSubview(snippetView)
        stderrView = readOnlyTextView(height: 50)
        stderrView.isHidden = true
        srv.addArrangedSubview(stderrView)
        root.addArrangedSubview(srv)

        let content = NSView()
        content.addSubview(root)
        NSLayoutConstraint.activate([
            root.leadingAnchor.constraint(equalTo: content.leadingAnchor, constant: 16),
            root.trailingAnchor.constraint(equalTo: content.trailingAnchor, constant: -16),
            root.topAnchor.constraint(equalTo: content.topAnchor, constant: 16),
        ])
        window?.contentView = content
    }

    private func stack(_ o: NSUserInterfaceLayoutOrientation) -> NSStackView {
        let s = NSStackView(); s.orientation = o; s.spacing = 6
        s.translatesAutoresizingMaskIntoConstraints = false
        return s
    }
    private func sectionTitle(_ t: String) -> NSTextField {
        let l = NSTextField(labelWithString: t)
        l.font = NSFont.boldSystemFont(ofSize: 13)
        return l
    }
    private func readOnlyTextView(height: CGFloat) -> NSTextView {
        let tv = NSTextView()
        tv.isEditable = false
        tv.font = NSFont.monospacedSystemFont(ofSize: 10, weight: .regular)
        tv.widthAnchor.constraint(equalToConstant: 420).isActive = true
        tv.heightAnchor.constraint(equalToConstant: height).isActive = true
        return tv
    }

    // MARK: - Live state (2 s timer)

    private func refresh() {
        for p in TCCPermission.allCases {
            permStatus[p]?.stringValue = p.isGranted ? "✓ granted" : "✗ missing"
        }
        grantButton.isHidden = !TCCPermission.allCases.contains { !$0.isGranted }

        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        switch server.state {
        case .running:               serverLabel.stringValue = "running on \(addr)"
        case .starting, .restarting: serverLabel.stringValue = "starting…"
        case .crashed:               serverLabel.stringValue = "crashed"
        case .stopped:               serverLabel.stringValue = "stopped"
        }
        stderrView.isHidden = server.state != .crashed
        if server.state == .crashed { stderrView.string = server.stderrText }
        snippetView.string = Snippet.opencodeEntry(addr: addr, token: token)

        let t = ConfigStore.target(from: json)
        rectLabel.stringValue = (t.mode == "rect" && t.w > 0)
            ? "(\(t.x), \(t.y), \(t.w), \(t.h))" : ""

        // Sync the segment ONLY when the stored mode actually changed, so a
        // user mid-selection in Rectangle mode isn't snapped back every 2 s.
        let key = (t.mode == "rect" && t.w > 0) ? "rect" : "app"
        if key != lastModeKey {
            lastModeKey = key
            segmented.selectedSegment = key == "rect" ? 1 : 0
            updateModeRows()
        }
    }

    private func updateModeRows() {
        pickerRow.isHidden = segmented.selectedSegment != 0
        rectRow.isHidden = segmented.selectedSegment != 1
    }

    // MARK: - Actions

    @objc private func requestPerm(_ sender: NSButton) { TCCPermission.allCases[sender.tag].request() }
    @objc private func grantAll() {
        for p in TCCPermission.allCases where !p.isGranted { p.request() }
    }

    @objc private func modeChanged() {
        lastModeKey = segmented.selectedSegment == 1 ? "rect" : "app"
        updateModeRows()
    }

    @objc private func refreshPicker() {
        picker.removeAllItems()
        picker.addItem(withTitle: "Loading…")
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        guard let url = URL(string: "http://\(addr)") else { return }
        let client = MCPClient(baseURL: url, token: token.isEmpty ? nil : token)
        Task { [weak self] in
            do {
                let text = try await client.callToolText("list_windows")
                await MainActor.run {
                    guard let self else { return }
                    self.picker.removeAllItems()
                    var n = 0
                    for line in text.components(separatedBy: "\n") where !line.isEmpty {
                        guard let w = WindowLine.parse(line) else { continue }
                        self.picker.addItem(withTitle: w.title.isEmpty ? w.app : "\(w.app) — \(w.title)")
                        self.picker.lastItem?.representedObject = w
                        n += 1
                    }
                    if n == 0 {
                        self.picker.addItem(withTitle: "(no windows — is the server running?)")
                    } else {
                        self.selectCurrentTargetInPicker()
                    }
                }
            } catch {
                await MainActor.run {
                    self?.picker.removeAllItems()
                    self?.picker.addItem(withTitle: "server error: \(error.localizedDescription)")
                }
            }
        }
    }

    private func selectCurrentTargetInPicker() {
        let t = ConfigStore.target(from: (try? ConfigStore.load()) ?? [:])
        guard !t.app.isEmpty else { return }
        for item in picker.itemArray {
            if let w = item.representedObject as? WindowEntry, w.app == t.app {
                picker.selectItem(item)
                return
            }
        }
    }

    @objc private func pickerChanged() {
        guard let w = picker.selectedItem?.representedObject as? WindowEntry else { return }
        saveTarget(ConfigStore.Target(app: w.app, title: w.title, mode: "app", x: 0, y: 0, w: 0, h: 0))
    }

    // T16 fix (T15 precedent): retain the overlay — a local `o` would deallocate
    // when selectRect() returns (onResult captures only [weak self]=controller),
    // so onResult would never fire on a real drag.
    private var rectOverlay: RectOverlay?

    @objc private func selectRect() {
        let o = RectOverlay()
        o.onResult = { [weak self] x, y, w, h in
            DispatchQueue.main.async {
                self?.saveTarget(ConfigStore.Target(app: "", title: "", mode: "rect",
                                                    x: x, y: y, w: w, h: h))
            }
        }
        rectOverlay = o
        o.present()
    }

    /// Load-modify-save; on a malformed config, ALERT and touch nothing.
    private func saveTarget(_ t: ConfigStore.Target) {
        do {
            let json = try ConfigStore.load()
            try ConfigStore.save(ConfigStore.applyTarget(t, to: json))
        } catch {
            let a = NSAlert()
            a.messageText = "Config write failed"
            a.informativeText = "\(error)\nThe app will not overwrite config.json — fix it by hand."
            a.runModal()
        }
        refresh()
    }

    @objc private func restartServer() { server.restart() }

    @objc private func copySnippet() {
        let json = (try? ConfigStore.load()) ?? [:]
        let (addr, token) = ConfigStore.httpConfig(from: json)
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(Snippet.opencodeEntry(addr: addr, token: token),
                                       forType: .string)
    }
}
```

- [ ] **Step 2: AppDelegate — remove DEV trigger, add Open item**

In `AppDelegate.swift`:
1. Delete the `DEV draw rect…` menu lines and the `devDraw()` method (Task 15 Step 2 additions).
2. Add the property and action:

```swift
    private var settingsWC: SettingsWindowController?

    @objc private func openSettings() {
        // T16 fix: replace a controller whose window was closed, so the old
        // one (and its now-invalidated timer) deallocates; reuse a visible one.
        if settingsWC == nil || settingsWC?.window == nil || !(settingsWC?.window?.isVisible ?? false) {
            settingsWC = SettingsWindowController(server: server)
        }
        settingsWC?.showWindow(nil)
        settingsWC?.window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
```

3. Extend the `rebuild()` action loop to include it first:

```swift
        for (label, sel) in [("Open ScreenReader…", #selector(openSettings)),
                             ("Restart server", #selector(restartServer)),
                             ("Test capture…", #selector(testCapture))] {
```

- [ ] **Step 3: Build + manual verification**

Run: `cd app && swift build && swift test`, then `./build.sh && open app/build/ScreenReader.app`, menu → **Open ScreenReader…**:
- All three permission rows show ✓/✗ correctly (compare with System Settings).
- Server section shows `running on 127.0.0.1:18427` + the snippet with token.
- **Refresh** lists windows; selecting one rewrites `app`/`title` in `~/.config/screenreader/config.json` (`cat` it), mode stays `app`, `ocr`/`capture`/`input` keys untouched.
- Segment → Rectangle → **Select rectangle…** → draw → config gains `mode: "rect"` + rect; label shows it; segment does NOT snap back on the next 2-s tick.
- **Copy opencode config** → `pbpaste` shows the snippet JSON.
- Quit-behavior: closing the window leaves the menu-bar item alive (app stays running).

- [ ] **Step 4: Commit**

```bash
git add app/Sources/ScreenReader/
git commit -m "feat(app): settings window — permissions, target picker, rect drawing, server"
```

---

### Task 17: Integration verification (manual, permission-gated)

**Files:** none — this task runs the spec §10 checklist on the dev Mac. Every step is a gate: if one fails, stop and fix before continuing. (The TCC-attribution canary already passed in Task 14; step 3 re-confirms it end-to-end.)

- [x] **Step 1: PAUSE — walk the checklist together with the user** — all 8 steps passed 2026-10-07 on build 6a1fbff3; issues found+fixed during the walk: Swift mode-persistence (0070436), list_windows CGWindowList fast path (80d186c/fa97033/d4aebbf); ad-hoc signing ⇒ 3 re-grant cycles (Task 6 not done; step 8 noted, not a failure).

Prereqs: Remote Desktop window open on screen; app built (`./build.sh`).

1. `open app/build/ScreenReader.app` — menu bar icon appears; status lines correct.
2. Open ScreenReader… → **Grant access** → approve the three prompts (Screen Recording, Accessibility, Automation). All rows go ✓ (relaunch if a status looks stale).
3. Menu → **Test capture…** → "Test capture OK" (re-run of the §7 canary through the full settings flow). Still ALL BLACK after correct granting → **STOP and escalate** spec §7 Option B; do not proceed.
4. Target → App window → Refresh → select the Remote Desktop window → config shows `app`/`title`, `mode: "app"`; menu bar shows the target line.
5. Rectangle mode → draw around a **known region** of the Remote Desktop window → `capture` via the endpoint (or the agent) with that rect and compare: the captured area must match the drawn box exactly (coordinate contract, spec §4.3). Also `ocr` a region and confirm text lands where the agent expects.
6. **Copy opencode config** → merge the fragment into `~/.config/opencode/opencode.json` under `mcp.servers` (replace the old `local`/stdio entry if present) → restart opencode → `/mcps` shows `screenreader` connected → have the agent run `capture` + `ocr` + a `dry_run: true` `click` — all succeed.
7. Quit the app → `curl -s http://127.0.0.1:18427/health` fails (endpoint gone); reopen → back within ~2s.
8. Rebuild (`./build.sh`) + reopen → **no permission re-prompt** and Test capture still OK (self-signed identity holds). If the user skipped Task 6, expect re-grants — note it, don't treat as failure.

- [x] **Step 2: Record the outcome**

Fix forward anything found; then:

```bash
git add -A && git commit -m "test: integration checklist results" || true
```

---

### Task 18: Documentation — README, AGENTS.md, TODO.md

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `TODO.md`

**Interfaces:** consumes everything (documentation of the shipped result).

- [ ] **Step 1: README — insert an "App" section after "## Status"**

````markdown
## ScreenReader app (recommended)

The **ScreenReader** menu-bar app hosts the server for you and owns the macOS
permissions — grant them once to the app instead of to every terminal.

```bash
./build.sh                       # builds server + app → app/build/ScreenReader.app
open app/build/ScreenReader.app
```

1. First run: click **Grant access** and approve the three prompts (Screen
   Recording, Accessibility, Automation). Confirm with menu → **Test capture…**.
2. Open the window → pick the target: select a window from the list, or draw a
   rectangle over the screen.
3. **Copy opencode config** → merge the snippet into your `opencode.json` under
   `mcp.servers`. Any agent then reaches the MCP endpoint at
   `http://127.0.0.1:18427/mcp` while the app is open.

Notes:

- The endpoint is **loopback-only** and protected by a bearer token stored in
  `~/.config/screenreader/config.json` (`http.token`) — rotate it by editing the
  file; it takes effect on the next request. Keep the snippet in the app window
  in sync after rotating.
- Quitting the app stops the MCP endpoint (start the app again to serve).
- One-time identity (recommended): Keychain Access → Certificate Assistant →
  Create a New Certificate → name **ScreenReader Dev**, kind Code Signing,
  self-signed, login keychain. Without it, builds are ad-hoc-signed and macOS
  asks for permissions again after every rebuild.
- The app writes only `app`/`title`/`mode`/`rect`/`http` keys to the shared
  config; `ocr`/`capture`/`input` edits keep working.
- Build/test: `cd app && swift build && swift test` (Kit logic is unit-tested;
  the GUI + TCC behavior is verified via the menu → Test capture canary).
````

- [ ] **Step 2: AGENTS.md — replace the refactor warning bullet**

Note: `AGENTS.md` and `TODO.md` are **not tracked by git** in this repo (keep them that way — edit in the workspace; do not `git add` them unless the user asks).

Replace the bullet starting `TODO.md describes a future Swift menu-bar app refactor` with:

```markdown
- The Swift menu-bar app is implemented per `docs/superpowers/specs/2026-10-06-screenreader-app-design.md` and `docs/superpowers/plans/2026-10-06-screenreader-app.md`: `app/` is a Swift package (`swift build`/`swift test`), `./build.sh` assembles + signs `app/build/ScreenReader.app` embedding the Go binary; the app spawns `screenreader --http` (loopback Streamable HTTP on :18427, bearer token in config `http.token`) and owns the TCC grants. The app mutates only `app`/`title`/`mode`/`rect`/`http` in config.json and preserves the other keys.
```

- [ ] **Step 3: TODO.md — mark the refactor done**

Change the top-level item and its sub-bullets from `- ` to `+ ` (the file's own convention: `+` = done).

- [ ] **Step 4: Final gate + commit**

```bash
cd server && CGO_ENABLED=1 go vet ./... && CGO_ENABLED=1 go test ./... && cd ..
(cd app && swift test)
./build.sh
git add README.md   # AGENTS.md/TODO.md edits stay untracked (see note above)
git commit -m "docs: app setup, permissions, endpoint + token notes"
```

Expected: all green; `built: app/build/ScreenReader.app`.

---

## Out of scope (do NOT do in this plan)

- Any change to the 17 MCP tools, coordinate math, OCR, capture, or input internals (spec §5: engine reused verbatim).
- Legacy SSE endpoint (`NewSSEHTTPHandler`), OAuth, LAN binding, multi-target (spec §14).
- Removing stdio mode or the existing README setup (backward compatible).
- Notarization / distribution / App Store.
