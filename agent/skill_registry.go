package agent

import (
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/tools"
)

func buildSkillRegistry(cfg *config.Config) (tools.SkillRegistry, int) {
	if cfg == nil || !cfg.Tools.Skills.EnabledValue() {
		return nil, 0
	}
	return tools.NewClawHubRegistry(tools.ClawHubRegistryConfig{
		BaseURL:          cfg.Tools.Skills.Registry.BaseURL,
		AuthToken:        cfg.Tools.Skills.Registry.AuthToken,
		SearchPath:       cfg.Tools.Skills.Registry.SearchPath,
		SkillsPath:       cfg.Tools.Skills.Registry.SkillsPath,
		DownloadPath:     cfg.Tools.Skills.Registry.DownloadPath,
		TimeoutSec:       cfg.Tools.Skills.Registry.TimeoutSec,
		MaxZipBytes:      cfg.Tools.Skills.Registry.MaxZipBytes,
		MaxResponseBytes: cfg.Tools.Skills.Registry.MaxResponseBytes,
	}), cfg.Tools.Skills.MaxResults
}
