package pilot

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// tool is a tool the model may call, with its JSON Schema.
type tool struct {
	name        string
	description string
	schema      map[string]any
}

// call is a tool call of the model's. bad says why its arguments could
// not be read; the call then goes back to the model as an error.
type call struct {
	id   string
	name string
	args map[string]any
	bad  string
}

// image is a picture in a tool's result: its bytes and their type.
type image struct {
	data []byte
	mime string
}

// result is a tool's result for the model.
type result struct {
	id      string
	text    string
	images  []image
	isError bool
}

// usage is what a request used, in tokens.
type usage struct {
	input      int64 // input not read from or written to the cache
	output     int64
	cacheRead  int64
	cacheWrite int64
}

// turn is the model's answer to a request.
type turn struct {
	text    string // what it wrote, if anything
	calls   []call
	refused bool
	usage   usage
}

// model is a conversation with a model over one API. It keeps the
// history: each send adds the results of the last turn's calls, then the
// text, if any, as the person's message.
type model interface {
	send(ctx context.Context, results []result, text string) (*turn, error)
}

// newModel starts a conversation with the model of cfg.
func newModel(cfg Config, key string, hc *http.Client, system string, tools []tool) (model, error) {
	if cfg.Provider == OpenAI {
		return newOpenAI(cfg, key, hc, system, tools), nil
	}
	return newClaude(cfg, key, hc, system, tools), nil
}

// Old screens leave the conversation when it grows past keepChars: the
// results of all but the last keepResults calls become cleared, all at
// once, so that a prompt cache keeps working until the next time. It is
// for APIs that do not clear them themselves (Anthropic's own API does,
// with context editing). About 4 characters make a token, so this keeps
// a conversation near 40k tokens.
const (
	keepChars   = 160_000
	keepResults = 5
	cleared     = "[Removed to save space. Call phone_observe to see the screen again.]"
)

// imageChars is what a screenshot counts as against keepChars: about the
// 1,500 tokens a phone's screenshot takes.
const imageChars = 6000

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
