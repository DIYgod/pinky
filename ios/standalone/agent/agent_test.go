package agent

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type noTools struct{ calls atomic.Int32 }

func (n *noTools) List() []Tool {
	return []Tool{{Name: "observe", Schema: map[string]any{"type": "object"}}}
}

func (n *noTools) Call(ctx context.Context, name string, args map[string]any) Result {
	n.calls.Add(1)
	return Result{Text: "screen"}
}

func serve(t *testing.T, h func(w http.ResponseWriter, n int)) *httptest.Server {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		h(w, int(n.Add(1)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNudgeThenFail(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, n int) {
		io.WriteString(w, `{"content":[{"type":"text","text":"I am thinking out loud"}],"stop_reason":"end_turn","usage":{}}`)
	})
	st := Start(Config{Key: "k", BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait()
	if st.End != Failed || st.Summary != "I am thinking out loud" {
		t.Fatalf("status = %+v", st)
	}
}

func TestRefusalAndBadKey(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, n int) {
		io.WriteString(w, `{"content":[],"stop_reason":"refusal","usage":{}}`)
	})
	if st := Start(Config{Key: "k", BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait(); st.End != Refused {
		t.Fatalf("refusal: %+v", st)
	}
	srv = serve(t, func(w http.ResponseWriter, n int) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"invalid x-api-key"}}`)
	})
	if st := Start(Config{Key: "k", BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait(); st.End != BadKey {
		t.Fatalf("401: %+v", st)
	}
	if st := Start(Config{BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait(); st.End != BadKey {
		t.Fatalf("no key: %+v", st)
	}
}

func TestStepLimit(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, n int) {
		io.WriteString(w, `{"content":[{"type":"tool_use","id":"t`+strings.Repeat("x", n)+`","name":"observe","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":100}}`)
	})
	tl := &noTools{}
	st := Start(Config{Key: "k", BaseURL: srv.URL, MaxSteps: 3}, "t", "s", tl, nil).Wait()
	if st.End != StepLimit || st.Steps != 3 || tl.calls.Load() != 3 {
		t.Fatalf("status = %+v, calls %d", st, tl.calls.Load())
	}
	if st.Cost <= 0 || st.Tokens != 300 {
		t.Fatalf("cost %v, tokens %d", st.Cost, st.Tokens)
	}
}

func TestRetryAndRedact(t *testing.T) {
	const key = "sk-secret"
	srv := serve(t, func(w http.ResponseWriter, n int) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":{"message":"bad key sk-secret here"}}`)
	})
	st := Start(Config{Key: key, BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait()
	if st.End != Error || strings.Contains(st.Summary, key) {
		t.Fatalf("status = %+v", st)
	}

	var tries atomic.Int32
	srv = serve(t, func(w http.ResponseWriter, n int) {
		tries.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"content":[{"type":"tool_use","id":"f","name":"finish","input":{"outcome":"done","summary":"ok"}}],"stop_reason":"tool_use","usage":{}}`)
	})
	if st := Start(Config{Key: key, BaseURL: srv.URL}, "t", "s", &noTools{}, nil).Wait(); st.End != Done || tries.Load() != 2 {
		t.Fatalf("status = %+v after %d tries", st, tries.Load())
	}
}

func TestRedactAPIError(t *testing.T) {
	err := redact(&apiError{status: 400, message: "key k123 is bad"}, "k123")
	var ae *apiError
	if !errors.As(err, &ae) || strings.Contains(err.Error(), "k123") {
		t.Fatalf("redact = %v", err)
	}
}
