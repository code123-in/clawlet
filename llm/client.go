package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	Provider    string
	BaseURL     string
	APIKey      string
	Model       string
	MaxTokens   int
	Temperature *float64
	Cooldown    time.Duration
	Headers     map[string]string
	HTTP         HTTPDoer
	SystemPrompt string // Base system prompt to prepend
	Verbose      bool   // Log LLM requests
	MaxRetries   int    // Max retries for 429 errors

	mu        sync.Mutex
	lastReqAt time.Time
}

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ChatResult struct {
	Content   string
	ToolCalls []ToolCall
}

func (r ChatResult) HasToolCalls() bool { return len(r.ToolCalls) > 0 }

func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 120 * time.Second}
	}

	// Active Rate Limiting (Cooldown)
	// Many providers (GLM-4-Flash, Gemini-Free) have tight concurrency records.
	// We ensure a minimum period between the START of requests from this client.
	cooldown := c.Cooldown
	if cooldown <= 0 {
		cooldown = 1 * time.Second // Default fallback
	}

	now := time.Now()
	elapsed := now.Sub(c.lastReqAt)
	if elapsed < cooldown {
		wait := cooldown - elapsed
		if c.Verbose {
			fmt.Printf("%s llm: cooling down for %s...\n", now.Format("15:04:05.000"), wait.Round(time.Millisecond))
		}
		time.Sleep(wait)
	}
	c.lastReqAt = time.Now()

	if c.Verbose {
		fmt.Printf("%s llm: request started provider=%s model=%s\n", c.lastReqAt.Format("15:04:05.000"), c.Provider, c.Model)
	}
	start := c.lastReqAt

	// Inject base system prompt if provided
	if strings.TrimSpace(c.SystemPrompt) != "" {
		messages = append([]Message{{Role: "system", Content: c.SystemPrompt}}, messages...)
	}

	maxTries := c.MaxRetries
	if maxTries <= 0 {
		maxTries = 3
	}

	var res *ChatResult
	var err error

	for try := 0; try <= maxTries; try++ {
		res, err = c.doChat(ctx, messages, tools)
		if err == nil {
			break
		}

		// Check for 429 Rate Limit or Timeouts
		errStr := strings.ToLower(err.Error())
		isRateLimit := strings.Contains(errStr, "429") || strings.Contains(errStr, "rate limit") || strings.Contains(errStr, "resource_exhausted")
		isTimeout := strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded")

		if isRateLimit || isTimeout {
			if try < maxTries {
				wait := c.computeWaitDuration(errStr, try)
				
				label := "rate limit"
				if isTimeout {
					label = "timeout"
				}

				fmt.Fprintf(os.Stderr, "warning: llm %s detected, retrying in %s (attempt %d/%d)...\n", label, wait, try+1, maxTries)
				
				if c.Verbose {
					fmt.Printf("%s llm: %s detected, retrying in %s (attempt %d/%d)...\n", time.Now().Format("15:04:05.000"), label, wait, try+1, maxTries)
				}
				
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
					continue
				}
			}
		}
		// If it's another error or we're out of retries, stop
		break
	}

	if c.Verbose {
		duration := time.Since(start).Round(time.Millisecond)
		prefix := time.Now().Format("15:04:05.000")
		if err != nil {
			fmt.Printf("%s llm: request failed after %s: %v\n", prefix, duration, err)
		} else {
			fmt.Printf("%s llm: request succeeded after %s\n", prefix, duration)
		}
	}

	return res, err
}

var retryAfterRegex = regexp.MustCompile(`(?i)reset after (\d+)s`)

func (c *Client) computeWaitDuration(errStr string, try int) time.Duration {
	// 1. Priority: Specific "reset after Xs" from error body (e.g. Antigravity)
	if matches := retryAfterRegex.FindStringSubmatch(errStr); len(matches) > 1 {
		if s, err := strconv.Atoi(matches[1]); err == nil {
			return time.Duration(s+1) * time.Second // Add 1s buffer
		}
	}

	// 2. Fallback: Exponential backoff
	// Attempt 0: 2s
	// Attempt 1: 4s
	// Attempt 2: 8s
	// ...
	seconds := 1 << (try + 1)
	return time.Duration(seconds) * time.Second
}

func (c *Client) doChat(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	switch normalizeProvider(c.Provider) {
	case "", "openai", "openrouter", "ollama":
		return c.chatOpenAICompatible(ctx, messages, tools)
	case "anthropic":
		return c.chatAnthropic(ctx, messages, tools)
	case "gemini":
		return c.chatGemini(ctx, messages, tools)
	case "antigravity":
		return c.chatAntigravity(ctx, messages, tools)
	default:
		return nil, fmt.Errorf("unsupported llm provider: %s", strings.TrimSpace(c.Provider))
	}
}

func normalizeProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "local":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(p))
	}
}

func (c *Client) maxTokensValue() int {
	if c.MaxTokens <= 0 {
		return 8192
	}
	return c.MaxTokens
}

func (c *Client) temperatureValue() *float64 {
	if c.Temperature != nil {
		v := *c.Temperature
		return &v
	}
	v := 0.7
	return &v
}
