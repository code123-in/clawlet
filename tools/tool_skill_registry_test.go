package tools

import (
	"context"
	"strings"
	"testing"
)

type mockSkillRegistry struct {
	searchFn  func(ctx context.Context, query string, limit int) ([]SkillSearchResult, error)
	installFn func(ctx context.Context, req SkillInstallRequest) (SkillInstallResult, error)
}

func (m mockSkillRegistry) Search(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
	return m.searchFn(ctx, query, limit)
}

func (m mockSkillRegistry) Install(ctx context.Context, req SkillInstallRequest) (SkillInstallResult, error) {
	return m.installFn(ctx, req)
}

func TestFindSkills(t *testing.T) {
	r := &Registry{
		WorkspaceDir:            t.TempDir(),
		SkillSearchDefaultLimit: 5,
		SkillRegistry: mockSkillRegistry{
			searchFn: func(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
				if query != "github" {
					t.Fatalf("unexpected query: %s", query)
				}
				if limit != 5 {
					t.Fatalf("unexpected limit: %d", limit)
				}
				return []SkillSearchResult{{
					Score:        0.95,
					Slug:         "github",
					DisplayName:  "GitHub",
					Summary:      "GitHub integration",
					Version:      "1.2.3",
					RegistryName: "clawhub",
				}}, nil
			},
		},
	}

	out, err := r.findSkills(context.Background(), "github", 0)
	if err != nil {
		t.Fatalf("findSkills failed: %v", err)
	}
	if !strings.Contains(out, "Found 1 skills") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "github v1.2.3") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestInstallSkill(t *testing.T) {
	workspace := t.TempDir()
	r := &Registry{
		WorkspaceDir: workspace,
		SkillRegistry: mockSkillRegistry{
			searchFn: nil,
			installFn: func(ctx context.Context, req SkillInstallRequest) (SkillInstallResult, error) {
				if req.Slug != "github" {
					t.Fatalf("unexpected slug: %s", req.Slug)
				}
				if req.RegistryName != "clawhub" {
					t.Fatalf("unexpected registry: %s", req.RegistryName)
				}
				if req.WorkspaceDir != workspace {
					t.Fatalf("unexpected workspace: %s", req.WorkspaceDir)
				}
				return SkillInstallResult{
					RegistryName: "clawhub",
					Slug:         "github",
					Version:      "1.2.3",
					InstallPath:  workspace + "/skills/github",
					Summary:      "GitHub integration",
				}, nil
			},
		},
	}

	out, err := r.installSkill(context.Background(), "github", "clawhub", "", false)
	if err != nil {
		t.Fatalf("installSkill failed: %v", err)
	}
	if !strings.Contains(out, "Installed skill \"github\" v1.2.3") {
		t.Fatalf("unexpected output: %s", out)
	}
}
