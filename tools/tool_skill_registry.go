package tools

import (
	"context"
	"fmt"
	"strings"
)

func (r *Registry) findSkills(ctx context.Context, query string, limit int) (string, error) {
	if r.SkillRegistry == nil {
		return "", fmt.Errorf("skill registry is not configured")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is empty")
	}
	if limit <= 0 {
		limit = r.SkillSearchDefaultLimit
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	results, err := r.SkillRegistry.Search(ctx, query, limit)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("No skills found for %q", query), nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d skills for %q:\n\n", len(results), query)
	for i, item := range results {
		fmt.Fprintf(&b, "%d. %s", i+1, item.Slug)
		if strings.TrimSpace(item.Version) != "" {
			fmt.Fprintf(&b, " v%s", item.Version)
		}
		fmt.Fprintf(&b, " (score: %.3f, registry: %s)\n", item.Score, item.RegistryName)
		if strings.TrimSpace(item.DisplayName) != "" && item.DisplayName != item.Slug {
			fmt.Fprintf(&b, "   Name: %s\n", item.DisplayName)
		}
		if strings.TrimSpace(item.Summary) != "" {
			fmt.Fprintf(&b, "   %s\n", item.Summary)
		}
		b.WriteByte('\n')
	}
	b.WriteString("Use install_skill with slug and registry to install.")
	return b.String(), nil
}

func (r *Registry) installSkill(ctx context.Context, slug, registryName, version string, force bool) (string, error) {
	if r.SkillRegistry == nil {
		return "", fmt.Errorf("skill registry is not configured")
	}

	r.skillInstallMu.Lock()
	defer r.skillInstallMu.Unlock()

	installed, err := r.SkillRegistry.Install(ctx, SkillInstallRequest{
		Slug:         slug,
		RegistryName: registryName,
		Version:      version,
		Force:        force,
		WorkspaceDir: r.WorkspaceDir,
	})
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if installed.IsSuspicious {
		b.WriteString("Warning: this skill is marked suspicious by registry moderation.\n\n")
	}
	fmt.Fprintf(&b, "Installed skill %q v%s from %s.\n", installed.Slug, installed.Version, installed.RegistryName)
	fmt.Fprintf(&b, "Location: %s\n", installed.InstallPath)
	if strings.TrimSpace(installed.Summary) != "" {
		fmt.Fprintf(&b, "Description: %s\n", installed.Summary)
	}
	b.WriteString("You can now load it with read_skill(name).")
	return b.String(), nil
}
