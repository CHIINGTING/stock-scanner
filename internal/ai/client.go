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
// Endpoint: POST /v1/responses, the Responses API — the interface OpenAI recommends for new
// projects. Two things differ from the older Chat Completions shape and both matter here:
//
//   - the output schema lives in text.format (type "json_schema"), NOT in response_format;
//     with strict:true the API itself guarantees the reply matches the schema, so the output
//     is machine-readable by construction rather than by asking nicely in the prompt;
//   - the reply is a TYPED output array, not a single choice. Reasoning, tool calls and
//     messages are separate item types, so the text has to be located by walking the items
//     rather than reading a fixed index. Nothing here is regex-scraped.
const (
	defaultBaseURL = "https://api.openai.com"
	responsesPath  = "/v1/responses"
	// maxResponseBytes caps what is read from the wire. A runaway or hostile response must
	// not be able to exhaust memory on a CI runner.
	maxResponseBytes = 1 << 20 // 1 MiB
	// apiKeyEnv is the ONLY place the token may come from.
	apiKeyEnv = "OPENAI_API_KEY"
)

// Client talks to the Responses endpoint.
//
// It holds no API key: the key is read from the environment inside Complete and used only to
// build one Authorization header. Nothing that could carry the secret is stored on the struct
// or returned to a caller.
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

// ── request ──────────────────────────────────────────────────────────────────────────

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type textFormat struct {
	Type   string         `json:"type"`
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
	Strict bool           `json:"strict"`
}

type textConfig struct {
	Format textFormat `json:"format"`
}

type responsesRequest struct {
	Model        string         `json:"model"`
	Instructions string         `json:"instructions,omitempty"`
	Input        []inputMessage `json:"input"`
	// Temperature is a pointer so a configured 0 can be omitted entirely. Some models reject
	// the parameter outright; setting temperature: 0 in config is the escape hatch that drops
	// it from the request rather than forcing a 400 on every call.
	Temperature *float64   `json:"temperature,omitempty"`
	Text        textConfig `json:"text"`
}

// analysisSchema is the strict JSON Schema the API enforces on the reply.
//
// strict:true requires every property to be listed in required and additionalProperties to be
// false — the model cannot omit a field or invent one. An empty array is how it says "no case
// to argue on this side", which is why nothing here is optional.
func analysisSchema() map[string]any {
	strArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary":    map[string]any{"type": "string"},
			"bull_case":  strArray,
			"bear_case":  strArray,
			"risk_flags": strArray,
			"confidence": map[string]any{"type": "number"},
		},
		"required":             []string{"summary", "bull_case", "bear_case", "risk_flags", "confidence"},
		"additionalProperties": false,
	}
}

// ── response ─────────────────────────────────────────────────────────────────────────

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type outputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type outputItem struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content []outputContent `json:"content"`
}

type responsesResponse struct {
	Status            string       `json:"status"`
	Output            []outputItem `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Error *apiError `json:"error"`
}

// completion is one raw model reply plus its token usage.
type completion struct {
	content string
	tokens  int
}

// Complete sends one request and returns the assistant's structured text.
//
// Every failure mode is an error here and becomes a non-OK Analysis upstream: missing key,
// context cancellation, timeout, non-2xx (401/429/5xx alike), an oversized or truncated body,
// undecodable JSON, an incomplete response, a refusal, or a well-formed response carrying no
// output text.
//
// Error strings deliberately never include the request body or the key.
func (c *Client) Complete(ctx context.Context, model, system, user string, temperature float64) (completion, error) {
	key := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if key == "" {
		return completion{}, fmt.Errorf("ai: %s is not set", apiKeyEnv)
	}

	reqBody := responsesRequest{
		Model:        model,
		Instructions: system,
		Input:        []inputMessage{{Role: "user", Content: user}},
		Text: textConfig{Format: textFormat{
			Type:   "json_schema",
			Name:   "stock_evidence_reading",
			Schema: analysisSchema(),
			Strict: true,
		}},
	}
	if temperature > 0 {
		reqBody.Temperature = &temperature
	}

	buf, err := json.Marshal(reqBody)
	if err != nil {
		return completion{}, fmt.Errorf("ai: encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+responsesPath, bytes.NewReader(buf))
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
		var errEnv responsesResponse
		if json.Unmarshal(body, &errEnv) == nil && errEnv.Error != nil {
			return completion{}, fmt.Errorf("ai: HTTP %d: %s (%s)",
				resp.StatusCode, errEnv.Error.Message, errEnv.Error.Type)
		}
		return completion{}, fmt.Errorf("ai: HTTP %d", resp.StatusCode)
	}

	var out responsesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return completion{}, fmt.Errorf("ai: decode response: %w", err)
	}
	if out.Error != nil {
		return completion{}, fmt.Errorf("ai: %s (%s)", out.Error.Message, out.Error.Type)
	}
	// A truncated reply can be valid JSON that is missing half the reading, so an incomplete
	// status is a failure rather than something to salvage.
	if out.Status == "incomplete" {
		reason := "unknown"
		if out.IncompleteDetails != nil && out.IncompleteDetails.Reason != "" {
			reason = out.IncompleteDetails.Reason
		}
		return completion{}, fmt.Errorf("ai: response incomplete (%s)", reason)
	}

	content, err := outputText(out.Output)
	if err != nil {
		return completion{}, err
	}
	return completion{content: content, tokens: out.Usage.TotalTokens}, nil
}

// outputText walks the typed output array and returns the assistant's text.
//
// The array can carry reasoning items, tool calls and messages in any order, so the text is
// located by TYPE rather than by position — indexing output[0] would break the moment a
// reasoning model is configured. A refusal is reported as such instead of being treated as an
// empty reply, because "the model declined" and "the model said nothing" are different facts.
func outputText(items []outputItem) (string, error) {
	var sb strings.Builder
	for _, it := range items {
		if it.Type != "message" {
			continue // reasoning / function_call / anything else this layer does not use
		}
		for _, part := range it.Content {
			switch part.Type {
			case "output_text":
				sb.WriteString(part.Text)
			case "refusal":
				return "", fmt.Errorf("ai: model refused: %s", strings.TrimSpace(part.Refusal))
			}
		}
	}
	text := strings.TrimSpace(sb.String())
	if text == "" {
		return "", fmt.Errorf("ai: response carried no output text")
	}
	return text, nil
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
