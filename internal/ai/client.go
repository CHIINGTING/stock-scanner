package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// OpenAI transport. Standard library only — no SDK, no new module dependency (go.mod still
// carries just yaml.v3 and x/text).
//
// Endpoint: POST /v1/chat/completions, the official Chat Completions API. It is not
// deprecated and supports response_format JSON mode, which is what keeps the output
// machine-readable instead of prose that would have to be regex-scraped.

const (
	defaultBaseURL = "https://api.openai.com"
	chatPath       = "/v1/chat/completions"
	// maxResponseBytes caps what is read from the wire. A runaway or hostile response must
	// not be able to exhaust memory on a CI runner.
	maxResponseBytes = 1 << 20 // 1 MiB
	// apiKeyEnv is the ONLY place the token may come from.
	apiKeyEnv = "OPENAI_API_KEY"
)

// Client talks to the Chat Completions endpoint.
//
// It holds no API key: the key is read from the environment inside Do and used only to build
// one Authorization header. Nothing that could carry the secret is stored on the struct or
// returned to a caller.
type Client struct {
	HTTP    *http.Client
	BaseURL string
}

// NewClient builds a client with the given per-request timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{HTTP: &http.Client{Timeout: timeout}, BaseURL: strings.TrimRight(baseURL, "/")}
}

// HasAPIKey reports whether the environment carries a key. Callers use it to skip the whole
// AI stage cleanly rather than issuing requests guaranteed to 401.
func HasAPIKey() bool { return strings.TrimSpace(os.Getenv(apiKeyEnv)) != "" }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model          string        `json:"model"`
	Messages       []chatMessage `json:"messages"`
	Temperature    float64       `json:"temperature"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// completion is one raw model reply plus its token usage.
type completion struct {
	content string
	tokens  int
}

// Complete sends one chat completion request and returns the assistant's content.
//
// Every failure mode is an error here and becomes a non-OK Analysis upstream: missing key,
// context cancellation, timeout, non-2xx (401/429/5xx alike), an oversized or truncated
// body, undecodable JSON, or a well-formed response carrying no content.
//
// Error strings deliberately never include the request body or the key.
func (c *Client) Complete(ctx context.Context, model, system, user string, temperature float64) (completion, error) {
	key := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if key == "" {
		return completion{}, fmt.Errorf("ai: %s is not set", apiKeyEnv)
	}

	reqBody := chatRequest{
		Model:       model,
		Temperature: temperature,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	// JSON mode: the model must return a single JSON object, so the reply can be decoded
	// instead of parsed out of prose.
	reqBody.ResponseFormat.Type = "json_object"

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return completion{}, fmt.Errorf("ai: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+chatPath, bytes.NewReader(buf))
	if err != nil {
		return completion{}, fmt.Errorf("ai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := c.http().Do(req)
	if err != nil {
		// Includes context cancellation and client timeout. The URL is safe to include; the
		// header is not, and is not part of this value.
		return completion{}, fmt.Errorf("ai: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return completion{}, fmt.Errorf("ai: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface the API's own message when it sent one — it names the real problem
		// (rate limit, bad key, model not found) far better than the status alone.
		var errEnv chatResponse
		if json.Unmarshal(body, &errEnv) == nil && errEnv.Error != nil {
			return completion{}, fmt.Errorf("ai: HTTP %d: %s (%s)",
				resp.StatusCode, errEnv.Error.Message, errEnv.Error.Type)
		}
		return completion{}, fmt.Errorf("ai: HTTP %d", resp.StatusCode)
	}

	var out chatResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return completion{}, fmt.Errorf("ai: decode response: %w", err)
	}
	if out.Error != nil {
		return completion{}, fmt.Errorf("ai: %s (%s)", out.Error.Message, out.Error.Type)
	}
	if len(out.Choices) == 0 {
		return completion{}, fmt.Errorf("ai: response carried no choices")
	}
	content := strings.TrimSpace(out.Choices[0].Message.Content)
	if content == "" {
		return completion{}, fmt.Errorf("ai: response carried empty content")
	}
	return completion{content: content, tokens: out.Usage.TotalTokens}, nil
}

func (c *Client) base() string {
	if c == nil || c.BaseURL == "" {
		return defaultBaseURL
	}
	return c.BaseURL
}

func (c *Client) http() *http.Client {
	if c == nil || c.HTTP == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTP
}
