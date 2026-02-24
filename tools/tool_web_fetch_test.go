package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAllowHostByPolicy_DefaultAllowAll(t *testing.T) {
	ok, reason := allowHostByPolicy("example.com", nil, nil)
	if !ok {
		t.Fatalf("expected allowed, reason=%s", reason)
	}
}

func TestAllowHostByPolicy_BlockedTakesPrecedence(t *testing.T) {
	ok, reason := allowHostByPolicy("api.example.com", []string{"*"}, []string{"example.com"})
	if ok {
		t.Fatalf("expected blocked")
	}
	if !strings.Contains(reason, "blocked") {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func TestAllowHostByPolicy_AllowSubdomain(t *testing.T) {
	ok, reason := allowHostByPolicy("api.example.com", []string{"example.com"}, nil)
	if !ok {
		t.Fatalf("expected allowed, reason=%s", reason)
	}
}

func TestAllowHostByPolicy_EmptyAllowListDenies(t *testing.T) {
	ok, reason := allowHostByPolicy("example.com", []string{}, nil)
	if ok {
		t.Fatalf("expected denied")
	}
	if !strings.Contains(reason, "no allowed domains") {
		t.Fatalf("unexpected reason: %s", reason)
	}
}

func newTestRegistry() *Registry {
	return &Registry{
		WorkspaceDir: "/tmp",
		ExecTimeout:  5 * time.Second,
	}
}

func TestWebFetch_BasicGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	out, err := r.webFetch(context.Background(), srv.URL, "text", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if status := result["status"].(float64); status != 200 {
		t.Fatalf("expected status 200, got %v", status)
	}
	if text := result["text"].(string); text != "hello world" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestWebFetch_HeadersForwarded(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	headers := map[string]string{"Authorization": "Bearer secret"}
	_, err := r.webFetch(context.Background(), srv.URL, "text", 0, headers)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected Authorization header to be forwarded, got %q", gotAuth)
	}
}

func TestWebFetch_NilHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	// nil headers must not panic
	_, err := r.webFetch(context.Background(), srv.URL, "text", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	r := newTestRegistry()
	_, err := r.webFetch(context.Background(), "", "text", 0, nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	_, err = r.webFetch(context.Background(), "ftp://example.com", "text", 0, nil)
	if err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

func TestWebFetch_RespectsResponseLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 4096)))
	}))
	defer server.Close()

	r := &Registry{
		WebFetchAllowedDomains: []string{"*"},
		WebFetchBlockedDomains: nil,
		WebFetchMaxResponse:    256,
		WebFetchTimeout:        5 * time.Second,
	}

	out, err := r.webFetch(context.Background(), server.URL, "text", 10000, nil)
	if err != nil {
		t.Fatalf("webFetch failed: %v", err)
	}
	var payload struct {
		Truncated         bool `json:"truncated"`
		ResponseTruncated bool `json:"responseTruncated"`
		Length            int  `json:"length"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
	if !payload.Truncated || !payload.ResponseTruncated {
		t.Fatalf("expected truncation flags, got %+v", payload)
	}
	if payload.Length > 256 {
		t.Fatalf("length exceeds response limit: %d", payload.Length)
	}
}

func TestWebFetch_DomainPolicyBlocks(t *testing.T) {
	r := &Registry{WebFetchAllowedDomains: []string{"example.com"}}
	_, err := r.webFetch(context.Background(), "https://openai.com", "text", 200, nil)
	if err == nil {
		t.Fatalf("expected policy error")
	}
	if !strings.Contains(err.Error(), "not in allowed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWebFetch_ExecuteDispatch(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r := newTestRegistry()
	args, _ := json.Marshal(map[string]any{
		"url":     srv.URL,
		"headers": map[string]string{"Accept": "application/json"},
	})
	out, err := r.Execute(context.Background(), Context{}, "web_fetch", args)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if gotAccept != "application/json" {
		t.Fatalf("expected Accept header forwarded, got %q", gotAccept)
	}
}
