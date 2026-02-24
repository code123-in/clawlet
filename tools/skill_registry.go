package tools

import (
	"context"
	"fmt"
	"strings"
)

type SkillSearchResult struct {
	Score        float64
	Slug         string
	DisplayName  string
	Summary      string
	Version      string
	RegistryName string
}

type SkillInstallRequest struct {
	Slug         string
	RegistryName string
	Version      string
	Force        bool
	WorkspaceDir string
}

type SkillInstallResult struct {
	RegistryName     string
	Slug             string
	Version          string
	Summary          string
	InstallPath      string
	IsSuspicious     bool
	IsMalwareBlocked bool
}

type SkillRegistry interface {
	Search(ctx context.Context, query string, limit int) ([]SkillSearchResult, error)
	Install(ctx context.Context, req SkillInstallRequest) (SkillInstallResult, error)
}

func validateSkillIdentifier(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("identifier is empty")
	}
	if strings.ContainsAny(trimmed, `/\\`) || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("identifier contains unsafe characters")
	}
	return trimmed, nil
}
