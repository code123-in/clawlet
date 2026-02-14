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
	Status  string `json:"status,omitempty"` // "ok", "not_found", "error"
}

func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	provider := normalizeProvider(c.Provider)
	switch provider {
	case "antigravity":
		// Antigravity (Cloud Code) doesn't have a public List API.
		// Return known working models for now.
		return []ModelInfo{
			{ID: "gemini-2.5-flash"},
			{ID: "gemini-2.5-pro"},
			{ID: "gemini-3-flash-preview"},
			{ID: "gemini-3-pro-preview"},
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

func (c *Client) ProbeModel(ctx context.Context, modelID string) (*ModelInfo, error) {
	// Create a temporary client with the specific model ID
	tmp := *c
	tmp.Model = modelID
	tmp.MaxRetries = 0 // Don't retry for probing
	tmp.Verbose = false

	if tmp.HTTP == nil {
		tmp.HTTP = &http.Client{Timeout: 30 * time.Second}
	}

	// Make a minimal "hi" request
	messages := []Message{{Role: "user", Content: "hi"}}
	_, err := tmp.doChat(ctx, messages, nil)

	info := &ModelInfo{ID: modelID}
	if err == nil {
		info.Status = "ok"
		return info, nil
	}

	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "404") || strings.Contains(errStr, "not found") {
		info.Status = "not_found"
	} else if strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") || strings.Contains(errStr, "exhausted") {
		// If it's a rate limit, the model exists but we can't use it right now
		info.Status = "ok"
	} else {
		info.Status = "error"
	}

	return info, err
}
