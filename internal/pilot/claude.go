package pilot

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// anthropicURL is the address of Anthropic's own API; tests change it.
var anthropicURL = "https://api.anthropic.com"

// claude is a conversation over the Messages API.
//
// On Anthropic's own API it uses what the API offers an agent like this:
// the prompt cache (the system prompt and tools never change during a
// task, and the history only grows), effort medium, context editing to
// clear old screens (clear_tool_uses, which is not an edit of the history,
// so the cache and the model's thinking stay valid) and, for the models
// that support it, server-side fallbacks, which give a request the model
// declines to another model rather than ending the task.
//
// At an address of the person's, a service that speaks the Messages API
// may know none of the betas, so it gets the plain API with the cache
// markers alone; Pinky clears old screens itself, unless the model is a
// Claude model, whose thinking an edited history would lose.
type claude struct {
	client  anthropic.Client
	params  anthropic.BetaMessageNewParams
	history []anthropic.BetaMessageParam
	trim    bool
}

// fallbackModels are the models that take server-side fallbacks.
var fallbackModels = []string{"claude-opus-5-5", "claude-opus-5", "claude-fable-5-2"}

func newClaude(cfg Config, key string, hc *http.Client, system string, tools []tool) *claude {
	official := cfg.BaseURL == "" || cfg.BaseURL == strings.TrimRight(anthropicURL, "/")
	base := cfg.BaseURL
	if base == "" {
		base = anthropicURL
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(base + "/"),
		option.WithAPIKey(key),
		option.WithHTTPClient(hc),
		option.WithMaxRetries(2),
		option.WithRequestTimeout(5 * time.Minute),
	}
	if !official {
		// Services that speak the Messages API read the key from either
		// header, as Claude Code's ANTHROPIC_API_KEY and
		// ANTHROPIC_AUTH_TOKEN send it.
		opts = append(opts, option.WithHeader("X-Api-Key", key))
	}
	c := &claude{client: anthropic.NewClient(opts...)}

	cache := anthropic.NewBetaCacheControlEphemeralParam()
	p := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(cfg.Model),
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: system, CacheControl: cache}},
	}
	for i, t := range tools {
		tp := &anthropic.BetaToolParam{
			Name:        t.name,
			Description: anthropic.String(t.description),
			InputSchema: inputSchema(t.schema),
		}
		if i == len(tools)-1 {
			tp.CacheControl = cache
		}
		p.Tools = append(p.Tools, anthropic.BetaToolUnionParam{OfTool: tp})
	}
	if official {
		// The conversation itself: the cache moves to its last block.
		p.CacheControl = cache
		if _, known := priceOf(cfg); known {
			p.OutputConfig = anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortMedium}
		}
		p.ContextManagement = anthropic.BetaContextManagementConfigParam{
			Edits: []anthropic.BetaContextManagementConfigEditUnionParam{{
				OfClearToolUses20250919: &anthropic.BetaClearToolUses20250919EditParam{
					Trigger: anthropic.BetaClearToolUses20250919EditTriggerUnionParam{
						OfInputTokens: &anthropic.BetaInputTokensTriggerParam{Value: keepChars / 4},
					},
					Keep:         anthropic.BetaToolUsesKeepParam{Value: keepResults},
					ClearAtLeast: anthropic.BetaInputTokensClearAtLeastParam{Value: keepChars / 4 / 2},
				},
			}},
		}
		p.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaContextManagement2025_06_27}
		for _, m := range fallbackModels {
			if cfg.Model == m || strings.HasPrefix(cfg.Model, m+"-2") {
				p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
				p.Betas = append(p.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
				break
			}
		}
	} else {
		c.trim = !strings.HasPrefix(strings.ToLower(cfg.Model), "claude")
	}
	c.params = p
	return c
}

// inputSchema turns a tool's JSON Schema into the API's.
func inputSchema(s map[string]any) anthropic.BetaToolInputSchemaParam {
	in := anthropic.BetaToolInputSchemaParam{Properties: s["properties"]}
	if in.Properties == nil {
		in.Properties = map[string]any{}
	}
	if req, ok := s["required"].([]any); ok {
		for _, r := range req {
			if r, ok := r.(string); ok {
				in.Required = append(in.Required, r)
			}
		}
	} else if req, ok := s["required"].([]string); ok {
		in.Required = req
	}
	for k, v := range s {
		switch k {
		case "type", "properties", "required", "$schema":
			continue
		}
		if in.ExtraFields == nil {
			in.ExtraFields = map[string]any{}
		}
		in.ExtraFields[k] = v
	}
	return in
}

func (c *claude) send(ctx context.Context, results []result, text string) (*turn, error) {
	var blocks []anthropic.BetaContentBlockParamUnion
	for _, r := range results {
		blocks = append(blocks, anthropic.BetaContentBlockParamUnion{OfToolResult: toolResult(r)})
	}
	if text != "" {
		blocks = append(blocks, anthropic.NewBetaTextBlock(text))
	}
	c.history = append(c.history, anthropic.NewBetaUserMessage(blocks...))
	if c.trim {
		c.clearOld()
	}
	p := c.params
	p.Messages = c.history
	resp, err := c.client.Beta.Messages.New(ctx, p)
	if err != nil {
		var ae *anthropic.Error
		if errors.As(err, &ae) {
			msg := ae.Message
			if msg == "" {
				msg = strings.TrimSpace(ae.RawJSON())
			}
			return nil, &apiError{status: ae.StatusCode, message: msg}
		}
		return nil, err
	}
	c.history = append(c.history, resp.ToParam())

	t := &turn{usage: usage{
		input:      resp.Usage.InputTokens,
		output:     resp.Usage.OutputTokens,
		cacheRead:  resp.Usage.CacheReadInputTokens,
		cacheWrite: resp.Usage.CacheCreationInputTokens,
	}}
	switch resp.StopReason {
	case anthropic.BetaStopReasonRefusal:
		t.refused = true
		return t, nil
	case anthropic.BetaStopReasonModelContextWindowExceeded:
		return t, errors.New("the task outgrew what the model can read at once")
	}
	var texts []string
	for _, b := range resp.Content {
		switch b := b.AsAny().(type) {
		case anthropic.BetaTextBlock:
			texts = append(texts, b.Text)
		case anthropic.BetaToolUseBlock:
			cl := call{id: b.ID, name: b.Name}
			if err := json.Unmarshal([]byte(b.JSON.Input.Raw()), &cl.args); err != nil {
				cl.bad = "The arguments are not a JSON object: " + err.Error()
			}
			t.calls = append(t.calls, cl)
		}
	}
	t.text = strings.Join(texts, "\n")
	return t, nil
}

func toolResult(r result) *anthropic.BetaToolResultBlockParam {
	tr := &anthropic.BetaToolResultBlockParam{ToolUseID: r.id}
	if r.isError {
		tr.IsError = anthropic.Bool(true)
	}
	if r.text != "" || len(r.images) == 0 {
		tr.Content = append(tr.Content, anthropic.BetaToolResultBlockParamContentUnion{
			OfText: &anthropic.BetaTextBlockParam{Text: r.text},
		})
	}
	for _, im := range r.images {
		mt := anthropic.BetaBase64ImageSourceMediaType(im.mime)
		if im.mime == "" {
			mt = anthropic.BetaBase64ImageSourceMediaTypeImageJPEG
		}
		tr.Content = append(tr.Content, anthropic.BetaToolResultBlockParamContentUnion{
			OfImage: &anthropic.BetaImageBlockParam{Source: anthropic.BetaImageBlockParamSourceUnion{
				OfBase64: &anthropic.BetaBase64ImageSourceParam{Data: base64.StdEncoding.EncodeToString(im.data), MediaType: mt},
			}},
		})
	}
	return tr
}

// clearOld clears old tool results once the history grows past keepChars.
func (c *claude) clearOld() {
	var trs []*anthropic.BetaToolResultBlockParam
	size := 0
	for i := range c.history {
		u := c.history[i].OfUser
		if u == nil {
			continue
		}
		for j := range u.Content {
			tr := u.Content[j].OfToolResult
			if tr == nil {
				continue
			}
			trs = append(trs, tr)
			for _, b := range tr.Content {
				switch {
				case b.OfText != nil:
					size += len(b.OfText.Text)
				case b.OfImage != nil:
					size += imageChars
				}
			}
		}
	}
	if size <= keepChars {
		return
	}
	for _, tr := range trs[:max(0, len(trs)-keepResults)] {
		tr.Content = []anthropic.BetaToolResultBlockParamContentUnion{{OfText: &anthropic.BetaTextBlockParam{Text: cleared}}}
	}
}
