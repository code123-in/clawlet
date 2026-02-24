package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClawHubRegistry_Search(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/search"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{"score": 0.9, "slug": "github", "displayName": "GitHub", "summary": "GitHub integration", "version": "1.2.3"},
					{"score": 0.8, "slug": "skip-me", "displayName": "Skip", "summary": ""},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClawHubRegistry(ClawHubRegistryConfig{BaseURL: ts.URL})
	results, err := client.Search(context.Background(), "github", 10)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Slug != "github" {
		t.Fatalf("unexpected slug: %s", results[0].Slug)
	}
}

func TestClawHubRegistry_Install(t *testing.T) {
	archive := mustZip(t, map[string]string{
		"SKILL.md":  "# github\n",
		"README.md": "hello\n",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/skills/github":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"slug":    "github",
				"summary": "GitHub integration",
				"latestVersion": map[string]any{
					"version": "1.2.3",
				},
				"moderation": map[string]any{
					"isSuspicious":     true,
					"isMalwareBlocked": false,
				},
			})
		case r.URL.Path == "/api/v1/download":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	workspace := t.TempDir()
	client := NewClawHubRegistry(ClawHubRegistryConfig{BaseURL: ts.URL})
	res, err := client.Install(context.Background(), SkillInstallRequest{
		Slug:         "github",
		RegistryName: "clawhub",
		WorkspaceDir: workspace,
	})
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if res.Version != "1.2.3" {
		t.Fatalf("unexpected version: %s", res.Version)
	}
	if !res.IsSuspicious {
		t.Fatalf("expected suspicious flag")
	}
	if _, err := os.Stat(filepath.Join(workspace, "skills", "github", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "skills", "github", ".skill-origin.json")); err != nil {
		t.Fatalf("origin metadata missing: %v", err)
	}
}

func TestClawHubRegistry_InstallRejectsTraversalZip(t *testing.T) {
	archive := mustZip(t, map[string]string{
		"../evil.txt": "owned",
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/skills/github":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"slug": "github",
				"latestVersion": map[string]any{
					"version": "1.0.0",
				},
			})
		case r.URL.Path == "/api/v1/download":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	workspace := t.TempDir()
	client := NewClawHubRegistry(ClawHubRegistryConfig{BaseURL: ts.URL})
	_, err := client.Install(context.Background(), SkillInstallRequest{
		Slug:         "github",
		RegistryName: "clawhub",
		WorkspaceDir: workspace,
	})
	if err == nil {
		t.Fatalf("expected traversal error")
	}
	if !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}
