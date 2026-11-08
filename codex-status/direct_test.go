package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnauthorized(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-token","account_id":"test-account"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://chatgpt.com/backend-api/wham/usage" || r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("ChatGPT-Account-Id") != "test-account" {
			t.Error("incorrect request")
		}
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	defer func() { http.DefaultTransport = old }()
	_, _, err := readDirect()
	if err == nil || !strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRedirectNotFollowed(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-token"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	followed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			followed = true
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()
	if _, _, err := readDirectURL(server.URL); err == nil {
		t.Fatal("redirect accepted")
	}
	if followed {
		t.Fatal("redirect followed")
	}
}

func TestEmptyRateLimitsFallBack(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-token"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"plan_type":"plus","rate_limit":{}}`)
	}))
	defer server.Close()
	called := false
	var warning strings.Builder
	_, _, err := fetchLimits(false, func() (json.RawMessage, json.RawMessage, error) {
		return readDirectURL(server.URL)
	}, func() (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{}`), nil
	}, &warning)
	if err != nil || !called || !strings.Contains(warning.String(), "unexpected quota response") {
		t.Fatalf("fallback: called=%v, error=%v, warning=%q", called, err, warning.String())
	}
}

func TestMissingAuth(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	if _, _, err := readDirect(); err == nil || !strings.Contains(err.Error(), "auth.json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTokenExpired(t *testing.T) {
	if !tokenExpired("header.eyJleHAiOjF9.signature") {
		t.Fatal("expired JWT was not detected")
	}
	if tokenExpired("not-a-jwt") {
		t.Fatal("opaque token was treated as expired")
	}
}

func TestConvertWindow(t *testing.T) {
	seconds := 301
	reset := int64(42)
	got := convertWindow(&apiWindow{Used: 12.5, Seconds: &seconds, Reset: &reset})
	if got.UsedPercent != 12.5 || got.Minutes == nil || *got.Minutes != 6 || got.ResetsAt == nil || *got.ResetsAt != reset {
		t.Fatalf("unexpected window: %+v", got)
	}
	if convertWindow(nil) != nil {
		t.Fatal("nil window must remain nil")
	}
}

func TestDirectFailureFallsBack(t *testing.T) {
	var warning strings.Builder
	called := false
	raw, normalized, err := fetchLimits(false, func() (json.RawMessage, json.RawMessage, error) {
		return nil, nil, errors.New("direct failed")
	}, func() (json.RawMessage, error) {
		called = true
		return json.RawMessage(`{"rateLimits":{}}`), nil
	}, &warning)
	if err != nil || !called || string(raw) != string(normalized) || !strings.Contains(warning.String(), "direct failed") {
		t.Fatalf("fallback failed: %v, %q", err, warning.String())
	}
}

func TestUnexpectedResponse(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"tokens":{"access_token":"test-token"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", home)
	old := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"unexpected":true}`)), Header: make(http.Header)}, nil
	})
	defer func() { http.DefaultTransport = old }()
	if _, _, err := readDirect(); err == nil {
		t.Fatal("unexpected response accepted")
	}
}
