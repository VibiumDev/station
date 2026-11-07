package jam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelComplete(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, `{"error":{"message":"no such path"}}`, 404)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"browser_tap","arguments":"{\"x\":10,\"y\":20}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":3}}`))
	}))
	defer srv.Close()
	m := &Model{Provider: "openai", Name: "gpt-test", BaseURL: srv.URL + "/v1", APIKey: "sk-secret", ReasoningEffort: "low"}
	reply, err := m.Complete(context.Background(), []Message{{Role: "user", Content: "tap"}},
		[]Tool{{Name: "browser_tap", Parameters: json.RawMessage(`{"type":"object"}`)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-secret" {
		t.Errorf("auth header: %q", auth)
	}
	if got["model"] != "gpt-test" || got["max_completion_tokens"] == nil || got["reasoning_effort"] != "low" {
		t.Errorf("request: %v", got)
	}
	if len(reply.Message.ToolCalls) != 1 || reply.Message.ToolCalls[0].Function.Name != "browser_tap" || reply.Usage.Prompt != 12 {
		t.Errorf("reply: %+v", reply)
	}
	// A provider error is reported with its message and without the key.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"Incorrect API key provided: sk-secret"}}`, 401)
	}))
	defer bad.Close()
	m.BaseURL = bad.URL
	_, err = m.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "sk-secret") {
		t.Errorf("error: %v", err)
	}
}

func TestEndpoint(t *testing.T) {
	cases := []struct{ provider, base, want string }{
		{"openai", "", "https://api.openai.com/v1"},
		{"google", "", "https://generativelanguage.googleapis.com/v1beta/openai"},
		{"google", "https://generativelanguage.googleapis.com/v1beta/openai", "https://generativelanguage.googleapis.com/v1beta/openai"},
		{"anthropic", "", "https://api.anthropic.com/v1"},
		{"openai-compatible", "http://box:8000/v1/", "http://box:8000/v1"},
	}
	for _, c := range cases {
		m := &Model{Provider: c.provider, BaseURL: c.base}
		if got := m.Endpoint(); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.provider, c.base, got, c.want)
		}
	}
}

func TestContentText(t *testing.T) {
	if contentText(json.RawMessage(`"hi"`)) != "hi" || contentText(json.RawMessage(`null`)) != "" ||
		contentText(json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)) != "a\nb" {
		t.Error("content shapes")
	}
}

func TestOpenAIEffortForTools(t *testing.T) {
	var efforts []any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		efforts = append(efforts, got["reasoning_effort"])
		calls++
		if got["model"] == "gpt-7-new" && got["reasoning_effort"] != "none" {
			http.Error(w, `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-7-new in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'."}}`, 400)
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"ready","arguments":"{\"word\":\"ready\"}"}}]},"finish_reason":"tool_calls"}],"usage":{}}`))
	}))
	defer srv.Close()
	tools := []Tool{{Name: "ready", Parameters: json.RawMessage(`{"type":"object"}`)}}
	// A gpt-5 model with no effort set gets none, as Vibium's table says.
	m := &Model{Provider: "openai", Name: "gpt-5.6-sol", BaseURL: srv.URL, APIKey: "k"}
	if _, err := m.Complete(context.Background(), []Message{{Role: "user", Content: "go"}}, tools, ""); err != nil {
		t.Fatal(err)
	}
	if efforts[0] != "none" {
		t.Errorf("gpt-5 effort: %v", efforts[0])
	}
	// An unlisted family that refuses is asked again with none.
	efforts, calls = nil, 0
	m.Name = "gpt-7-new"
	if _, err := m.Complete(context.Background(), []Message{{Role: "user", Content: "go"}}, tools, ""); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || efforts[0] != nil || efforts[1] != "none" {
		t.Errorf("retry: %d calls, efforts %v", calls, efforts)
	}
	// The ping goes through tools, so it finds this class of trouble.
	if word, err := m.Ping(context.Background()); err != nil || word != "ready" {
		t.Errorf("ping: %q, %v", word, err)
	}
	if _, err := (&Model{Provider: "openai", Name: "gpt-6.1-pro"}).Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "Responses API") {
		t.Errorf("a model without chat tools: %v", err)
	}
}
