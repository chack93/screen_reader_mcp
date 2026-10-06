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
