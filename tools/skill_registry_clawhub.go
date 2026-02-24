package tools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSkillRegistryTimeoutSec       = 30
	defaultSkillRegistryMaxZipBytes      = int64(50 << 20)
	defaultSkillRegistryMaxResponseBytes = int64(2 << 20)
	maxSkillZipEntryBytes                = int64(8 << 20)
)

type ClawHubRegistryConfig struct {
	BaseURL          string
	AuthToken        string
	SearchPath       string
	SkillsPath       string
	DownloadPath     string
	TimeoutSec       int
	MaxZipBytes      int64
	MaxResponseBytes int64
}

type ClawHubRegistry struct {
	baseURL          string
	authToken        string
	searchPath       string
	skillsPath       string
	downloadPath     string
	maxZipBytes      int64
	maxResponseBytes int64
	client           *http.Client
}

func NewClawHubRegistry(cfg ClawHubRegistryConfig) *ClawHubRegistry {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "https://clawhub.ai"
	}
	searchPath := strings.TrimSpace(cfg.SearchPath)
	if searchPath == "" {
		searchPath = "/api/v1/search"
	}
	skillsPath := strings.TrimSpace(cfg.SkillsPath)
	if skillsPath == "" {
		skillsPath = "/api/v1/skills"
	}
	downloadPath := strings.TrimSpace(cfg.DownloadPath)
	if downloadPath == "" {
		downloadPath = "/api/v1/download"
	}
	timeoutSec := cfg.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultSkillRegistryTimeoutSec
	}
	maxZipBytes := cfg.MaxZipBytes
	if maxZipBytes <= 0 {
		maxZipBytes = defaultSkillRegistryMaxZipBytes
	}
	maxResponseBytes := cfg.MaxResponseBytes
	if maxResponseBytes <= 0 {
		maxResponseBytes = defaultSkillRegistryMaxResponseBytes
	}

	return &ClawHubRegistry{
		baseURL:          strings.TrimRight(baseURL, "/"),
		authToken:        strings.TrimSpace(cfg.AuthToken),
		searchPath:       searchPath,
		skillsPath:       skillsPath,
		downloadPath:     downloadPath,
		maxZipBytes:      maxZipBytes,
		maxResponseBytes: maxResponseBytes,
		client: &http.Client{
			Timeout: time.Duration(timeoutSec) * time.Second,
		},
	}
}

type clawHubSearchResponse struct {
	Results []clawHubSearchResult `json:"results"`
}

type clawHubSearchResult struct {
	Score       float64 `json:"score"`
	Slug        *string `json:"slug"`
	DisplayName *string `json:"displayName"`
	Summary     *string `json:"summary"`
	Version     *string `json:"version"`
}

func (c *ClawHubRegistry) Search(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	u, err := c.buildURL(c.searchPath)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("limit", fmt.Sprintf("%d", limit))
	u.RawQuery = q.Encode()

	body, err := c.get(ctx, u.String())
	if err != nil {
		return nil, err
	}

	var resp clawHubSearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	out := make([]SkillSearchResult, 0, len(resp.Results))
	for _, item := range resp.Results {
		slug := strings.TrimSpace(deref(item.Slug))
		if slug == "" {
			continue
		}
		summary := strings.TrimSpace(deref(item.Summary))
		if summary == "" {
			continue
		}
		displayName := strings.TrimSpace(deref(item.DisplayName))
		if displayName == "" {
			displayName = slug
		}
		out = append(out, SkillSearchResult{
			Score:        item.Score,
			Slug:         slug,
			DisplayName:  displayName,
			Summary:      summary,
			Version:      strings.TrimSpace(deref(item.Version)),
			RegistryName: "clawhub",
		})
	}
	if len(out) == 0 {
		return out, nil
	}
	// Defensive sorting in case API order changes.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type clawHubSkillResponse struct {
	Slug          string                  `json:"slug"`
	DisplayName   string                  `json:"displayName"`
	Summary       string                  `json:"summary"`
	LatestVersion *clawHubVersionInfo     `json:"latestVersion"`
	Moderation    *clawHubModerationState `json:"moderation"`
}

type clawHubVersionInfo struct {
	Version string `json:"version"`
}

type clawHubModerationState struct {
	IsMalwareBlocked bool `json:"isMalwareBlocked"`
	IsSuspicious     bool `json:"isSuspicious"`
}

func (c *ClawHubRegistry) Install(ctx context.Context, req SkillInstallRequest) (SkillInstallResult, error) {
	slug, err := validateSkillIdentifier(req.Slug)
	if err != nil {
		return SkillInstallResult{}, fmt.Errorf("invalid slug: %w", err)
	}
	registryName, err := validateSkillIdentifier(req.RegistryName)
	if err != nil {
		return SkillInstallResult{}, fmt.Errorf("invalid registry: %w", err)
	}
	if registryName != "clawhub" {
		return SkillInstallResult{}, fmt.Errorf("unsupported registry: %s", registryName)
	}
	workspace := strings.TrimSpace(req.WorkspaceDir)
	if workspace == "" {
		return SkillInstallResult{}, fmt.Errorf("workspace is empty")
	}
	workspaceAbs, err := filepath.Abs(workspace)
	if err != nil {
		return SkillInstallResult{}, err
	}
	version := strings.TrimSpace(req.Version)

	skillsDir := filepath.Join(workspaceAbs, "skills")
	targetDir := filepath.Join(skillsDir, slug)

	if _, err := os.Stat(targetDir); err == nil {
		if !req.Force {
			return SkillInstallResult{}, fmt.Errorf("skill %q already installed (use force=true to reinstall)", slug)
		}
		if err := os.RemoveAll(targetDir); err != nil {
			return SkillInstallResult{}, fmt.Errorf("failed to remove existing skill: %w", err)
		}
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return SkillInstallResult{}, fmt.Errorf("failed to create skill directory: %w", err)
	}

	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(targetDir)
		}
	}()

	meta, _ := c.fetchSkillMeta(ctx, slug)
	result := SkillInstallResult{
		RegistryName: "clawhub",
		Slug:         slug,
		InstallPath:  targetDir,
	}
	if meta != nil {
		result.Summary = strings.TrimSpace(meta.Summary)
		if meta.Moderation != nil {
			result.IsSuspicious = meta.Moderation.IsSuspicious
			result.IsMalwareBlocked = meta.Moderation.IsMalwareBlocked
		}
	}
	if result.IsMalwareBlocked {
		return SkillInstallResult{}, fmt.Errorf("skill %q is flagged as malware and cannot be installed", slug)
	}
	if version == "" {
		if meta != nil && meta.LatestVersion != nil {
			version = strings.TrimSpace(meta.LatestVersion.Version)
		}
		if version == "" {
			version = "latest"
		}
	}
	result.Version = version

	zipPath, err := c.downloadSkillArchive(ctx, slug, version)
	if err != nil {
		return SkillInstallResult{}, err
	}
	defer os.Remove(zipPath)

	if err := extractZipSecure(zipPath, targetDir); err != nil {
		return SkillInstallResult{}, err
	}
	if err := normalizeSkillLayout(targetDir); err != nil {
		return SkillInstallResult{}, err
	}
	if _, err := os.Stat(filepath.Join(targetDir, "SKILL.md")); err != nil {
		return SkillInstallResult{}, fmt.Errorf("installed archive does not contain SKILL.md")
	}
	if err := writeSkillOrigin(targetDir, result.RegistryName, result.Slug, result.Version); err != nil {
		return SkillInstallResult{}, fmt.Errorf("failed to write skill metadata: %w", err)
	}

	cleanup = false
	return result, nil
}

func (c *ClawHubRegistry) fetchSkillMeta(ctx context.Context, slug string) (*clawHubSkillResponse, error) {
	u, err := c.buildURL(c.skillsPath + "/" + url.PathEscape(slug))
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, u.String())
	if err != nil {
		return nil, err
	}
	var resp clawHubSkillResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse skill metadata: %w", err)
	}
	return &resp, nil
}

func (c *ClawHubRegistry) downloadSkillArchive(ctx context.Context, slug, version string) (string, error) {
	u, err := c.buildURL(c.downloadPath)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("slug", slug)
	if version != "latest" {
		q.Set("version", version)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("download failed: http %d: %s", resp.StatusCode, string(body))
	}

	tmp, err := os.CreateTemp("", "clawlet-skill-*.zip")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, c.maxZipBytes+1))
	if err != nil {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("failed to save downloaded archive: %w", err)
	}
	if written > c.maxZipBytes {
		_ = os.Remove(tmp.Name())
		return "", fmt.Errorf("downloaded archive exceeds size limit")
	}
	return tmp.Name(), nil
}

func (c *ClawHubRegistry) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxResponseBytes {
		return nil, fmt.Errorf("response too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func (c *ClawHubRegistry) buildURL(path string) (*url.URL, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid registry baseURL: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")
	base.RawQuery = ""
	base.Fragment = ""
	return base, nil
}

func extractZipSecure(zipPath, targetDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("invalid zip archive: %w", err)
	}
	defer zr.Close()

	targetClean := filepath.Clean(targetDir)
	for _, entry := range zr.File {
		name := filepath.Clean(entry.Name)
		if name == "." {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("zip entry has unsafe path: %s", entry.Name)
		}
		dest := filepath.Join(targetClean, name)
		if !isSameOrChildPath(dest, targetClean) {
			return fmt.Errorf("zip entry escapes target directory: %s", entry.Name)
		}

		mode := entry.FileInfo().Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("zip entry %q is a symlink and is not allowed", entry.Name)
		}
		if mode.IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxSkillZipEntryBytes) {
			return fmt.Errorf("zip entry %q is too large", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		written, copyErr := io.Copy(out, io.LimitReader(rc, maxSkillZipEntryBytes+1))
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			_ = os.Remove(dest)
			return copyErr
		}
		if closeErr != nil {
			_ = os.Remove(dest)
			return closeErr
		}
		if written > maxSkillZipEntryBytes {
			_ = os.Remove(dest)
			return fmt.Errorf("zip entry %q exceeds maximum size", entry.Name)
		}
	}
	return nil
}

func normalizeSkillLayout(targetDir string) error {
	if _, err := os.Stat(filepath.Join(targetDir, "SKILL.md")); err == nil {
		return nil
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		return err
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}
	inner := filepath.Join(targetDir, entries[0].Name())
	if _, err := os.Stat(filepath.Join(inner, "SKILL.md")); err != nil {
		return nil
	}
	innerEntries, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, entry := range innerEntries {
		src := filepath.Join(inner, entry.Name())
		dst := filepath.Join(targetDir, entry.Name())
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("archive contains conflicting path: %s", entry.Name())
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.Remove(inner)
}

func writeSkillOrigin(targetDir, registryName, slug, version string) error {
	type origin struct {
		Version          int    `json:"version"`
		Registry         string `json:"registry"`
		Slug             string `json:"slug"`
		InstalledVersion string `json:"installed_version"`
		InstalledAt      int64  `json:"installed_at"`
	}
	payload := origin{
		Version:          1,
		Registry:         registryName,
		Slug:             slug,
		InstalledVersion: version,
		InstalledAt:      time.Now().UnixMilli(),
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(filepath.Join(targetDir, ".skill-origin.json"), b, 0o644)
}

func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
