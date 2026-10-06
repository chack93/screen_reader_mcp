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
