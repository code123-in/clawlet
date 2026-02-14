package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ModelInfo struct {
	ID      string `json:"id"`
	Created int64  `json:"created,omitempty"`
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	provider := normalizeProvider(c.Provider)
	switch provider {
	case "antigravity":
		// Antigravity (Cloud Code) doesn't have a public List API.
		// Return known working models for now.
		return []ModelInfo{
			{ID: "gemini-2.0-flash-exp"},
			{ID: "gemini-1.5-pro"},
			{ID: "gemini-1.5-flash"},
			{ID: "gemini-3-flash-preview"},
		}, nil
	case "", "openai", "openrouter", "ollama":
		return c.listOpenAICompatible(ctx)
	default:
		return nil, fmt.Errorf("listing models is not supported for provider: %s", c.Provider)
	}
}

func (c *Client) listOpenAICompatible(ctx context.Context) ([]ModelInfo, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(body))
	}

	var parsed struct {
		Data []ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data, nil
}
