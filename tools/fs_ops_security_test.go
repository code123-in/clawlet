package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePath_BlocksRootPath(t *testing.T) {
	r := &Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: false,
	}
	if _, err := r.resolvePath("/"); err == nil {
		t.Fatalf("expected root path to be blocked")
	}
}

func TestResolvePath_BlocksSensitiveStatePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("home dir is not available")
	}

	r := &Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: false,
	}
	sensitive := filepath.Join(home, ".clawlet", "auth", "openai-codex.json")
	if _, err := r.resolvePath(sensitive); err == nil {
		t.Fatalf("expected sensitive path to be blocked: %s", sensitive)
	}
}

func TestReadFile_BlocksSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior varies on windows")
	}

	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside.txt")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "leak.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := &Registry{
		WorkspaceDir:        ws,
		RestrictToWorkspace: true,
	}
	if _, err := r.readFile("leak.txt"); err == nil {
		t.Fatalf("expected symlink escape to be blocked")
	}
}

func TestWriteFile_BlocksSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior varies on windows")
	}

	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside.txt")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(outside, []byte("original"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(ws, "link.txt")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	r := &Registry{
		WorkspaceDir:        ws,
		RestrictToWorkspace: true,
	}
	if _, err := r.writeFile("link.txt", "overwrite"); err == nil {
		t.Fatalf("expected symlink target write to be blocked")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("outside file was modified: %q", string(got))
	}
}
