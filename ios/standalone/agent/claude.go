package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The Messages API as Ask Pinky speaks it (internal/pilot on the
// builtin-agent branch), cut down to Anthropic's own API: plain HTTP, the
// prompt cache, effort medium, context editing that clears old screens, and
// server-side fallbacks for the models that take them.

// DefaultURL is Anthropic's API.
const DefaultURL = "https://api.anthropic.com"

const anthropicVersion = "2026-10-21"

// keepTokens is about where old screens start to leave the conversation;
// keepResults is how many recent tool results stay.
const (
	keepTokens  = 40_000
	keepResults = 5
)

var fallbackModels = []string{"claude-opus-5-5", "claude-opus-5", "claude-fable-5-2"}

type (
	anRequest struct {
		Model             string      `json:"model"`
		MaxTokens         int         `json:"max_tokens"`
		System            []anText    `json:"system"`
		Tools             []anTool    `json:"tools,omitempty"`
		Messages          []anMessage `json:"messages"`
		CacheControl      *anCache    `json:"cache_control,omitempty"`
		OutputConfig      *anOutput   `json:"output_config,omitempty"`
		ContextManagement *anContext  `json:"context_management,omitempty"`
		Fallbacks         string      `json:"fallbacks,omitempty"`
	}
	anCache struct {
		Type string `json:"type"`
	}
	anOutput struct {
		Effort string `json:"effort"`
	}
	anTool struct {
		Name         string         `json:"name"`
		Description  string         `json:"description,omitempty"`
		InputSchema  map[string]any `json:"input_schema"`
		CacheControl *anCache       `json:"cache_control,omitempty"`
	}
	anContext struct {
		Edits []anEdit `json:"edits"`
	}
	anEdit struct {
		Type         string   `json:"type"`
		Trigger      anAmount `json:"trigger"`
		Keep         anAmount `json:"keep"`
		ClearAtLeast anAmount `json:"clear_at_least"`
	}
	anAmount struct {
		Type  string `json:"type"`
		Value int    `json:"value"`
	}
	anMessage struct {
		Role    string `json:"role"`
		Content []any  `json:"content"`
	}
	anText struct {
		Type         string   `json:"type"`
		Text         string   `json:"text"`
		CacheControl *anCache `json:"cache_control,omitempty"`
	}
	anImage struct {
		Type   string        `json:"type"`
		Source anImageSource `json:"source"`
	}
	anImageSource struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	}
	anToolResult struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   []any  `json:"content"`
		IsError   bool   `json:"is_error,omitempty"`
	}
	anResponse struct {
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      struct {
			InputTokens              int64 `json:"input_tokens"`
			OutputTokens             int64 `json:"output_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
)

var ephemeral = &anCache{Type: "ephemeral"}

// call is a tool call of the model's.
type call struct {
	id   string
	name string
	args map[string]any
	bad  string
}

type usage struct{ input, output, cacheRead, cacheWrite int64 }

type turn struct {
	text    string
	calls   []call
	refused bool
	usage   usage
}

// apiError is an error the API answered with.
type apiError struct {
	status     int
	message    string
	retryAfter time.Duration
}

func (e *apiError) Error() string {
	s := fmt.Sprintf("the API answered %d %s", e.status, http.StatusText(e.status))
	if e.message != "" {
		s += ": " + e.message
	}
	return s
}

// claude is one conversation.
type claude struct {
	url     string
	key     string
	hc      *http.Client
	req     anRequest
	betas   []string
	history []anMessage
}

func newClaude(cfg Config, system string, tools []Tool) *claude {
	base := cfg.BaseURL
	if base == "" {
		base = DefaultURL
	}
	c := &claude{url: strings.TrimRight(base, "/") + "/v1/messages", key: cfg.Key, hc: cfg.HTTP}
	r := anRequest{
		Model:        cfg.Model,
		MaxTokens:    16000,
		System:       []anText{{Type: "text", Text: system, CacheControl: ephemeral}},
		CacheControl: ephemeral,
		OutputConfig: &anOutput{Effort: "medium"},
		ContextManagement: &anContext{Edits: []anEdit{{
			Type:         "clear_tool_uses_20250919",
			Trigger:      anAmount{Type: "input_tokens", Value: keepTokens},
			Keep:         anAmount{Type: "tool_uses", Value: keepResults},
			ClearAtLeast: anAmount{Type: "input_tokens", Value: keepTokens / 2},
		}}},
	}
	for i, t := range tools {
		at := anTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
		if i == len(tools)-1 {
			at.CacheControl = ephemeral
		}
		r.Tools = append(r.Tools, at)
	}
	c.betas = []string{"context-management-2025-06-27"}
	if slices.ContainsFunc(fallbackModels, func(m string) bool { return cfg.Model == m || strings.HasPrefix(cfg.Model, m+"-2") }) {
		r.Fallbacks = "default"
		c.betas = append(c.betas, "server-side-fallback-2026-07-01")
	}
	c.req = r
	return c
}

func (c *claude) header() http.Header {
	h := http.Header{}
	h.Set("Anthropic-Version", anthropicVersion)
	h.Set("Authorization", "Bearer "+c.key)
	h.Set("Anthropic-Beta", strings.Join(c.betas, ","))
	h.Set("Content-Type", "application/json")
	return h
}

func (c *claude) send(ctx context.Context, results []callResult, text string) (*turn, error) {
	var blocks []any
	for _, r := range results {
		blocks = append(blocks, toolResult(r))
	}
	if text != "" {
		blocks = append(blocks, anText{Type: "text", Text: text})
	}
	c.history = append(c.history, anMessage{Role: "user", Content: blocks})
	r := c.req
	r.Messages = c.history
	body, err := marshal(r)
	if err != nil {
		return nil, err
	}
	data, err := post(ctx, c.hc, c.url, c.header(), body)
	if err != nil {
		return nil, err
	}
	var resp anResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("the API's answer is not JSON: %w", err)
	}
	if content := echo(resp.Content); len(content) > 0 {
		c.history = append(c.history, anMessage{Role: "assistant", Content: content})
	}
	t := &turn{usage: usage{resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.CacheReadInputTokens, resp.Usage.CacheCreationInputTokens}}
	switch resp.StopReason {
	case "refusal":
		t.refused = true
		return t, nil
	case "model_context_window_exceeded":
		return t, errors.New("the task outgrew what the model can read at once")
	}
	var texts []string
	for _, raw := range resp.Content {
		var b struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			cl := call{id: b.ID, name: b.Name}
			if len(b.Input) > 0 {
				if err := json.Unmarshal(b.Input, &cl.args); err != nil {
					cl.bad = "The arguments are not a JSON object: " + err.Error()
				}
			}
			t.calls = append(t.calls, cl)
		}
	}
	t.text = strings.Join(texts, "\n")
	return t, nil
}

// echo is the model's content as the next request sends it back, less null
// fields; after a fallback only the text of the declining model's blocks
// goes back.
func echo(content []json.RawMessage) []any {
	typ := func(raw json.RawMessage) string {
		var b struct {
			Type string `json:"type"`
		}
		json.Unmarshal(raw, &b)
		return b.Type
	}
	last := -1
	for i, raw := range content {
		if typ(raw) == "fallback" {
			last = i
		}
	}
	out := make([]any, 0, len(content))
	for i, raw := range content {
		if t := typ(raw); i < last && t != "text" && t != "fallback" {
			continue
		}
		out = append(out, withoutNulls(raw))
	}
	return out
}

func withoutNulls(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	n := len(m)
	for k, v := range m {
		if string(v) == "null" {
			delete(m, k)
		}
	}
	if len(m) == n {
		return raw
	}
	b, err := marshal(m)
	if err != nil {
		return raw
	}
	return b
}

func toolResult(r callResult) *anToolResult {
	tr := &anToolResult{Type: "tool_result", ToolUseID: r.id, IsError: r.IsError}
	if r.Text != "" || len(r.Images) == 0 {
		tr.Content = append(tr.Content, anText{Type: "text", Text: r.Text})
	}
	for _, im := range r.Images {
		tr.Content = append(tr.Content, anImage{Type: "image", Source: anImageSource{
			Type: "base64", MediaType: im.MIME, Data: base64.StdEncoding.EncodeToString(im.Data),
		}})
	}
	return tr
}

// post sends a request and returns the answer's body, trying twice more
// after a rate limit, a server error or a dropped connection.
func post(ctx context.Context, hc *http.Client, url string, header http.Header, body []byte) ([]byte, error) {
	wait := time.Second
	for try := 0; ; try++ {
		data, retry, err := postOnce(ctx, hc, url, header, body)
		if err == nil || !retry || try == 2 {
			return data, err
		}
		var ae *apiError
		if errors.As(err, &ae) && ae.retryAfter > 0 && ae.retryAfter <= 30*time.Second {
			wait = ae.retryAfter
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 3
	}
}

func postOnce(ctx context.Context, hc *http.Client, url string, header http.Header, body []byte) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header = header.Clone()
	resp, err := hc.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode/100 != 2 {
		ae := &apiError{status: resp.StatusCode, message: errorMessage(data)}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			ae.retryAfter = time.Duration(s) * time.Second
		}
		return nil, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, ae
	}
	return data, false, nil
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func errorMessage(data []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	s := strings.TrimSpace(string(data))
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + "…"
	}
	return s
}
