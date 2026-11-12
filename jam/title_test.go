package jam

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTidyTitle(t *testing.T) {
	for give, want := range map[string]string{
		"Soundcheck microphone echo":                "Soundcheck microphone echo",
		"\"Soundcheck microphone echo.\"":           "Soundcheck microphone echo",
		"**Check the clock**\n\nIt reads the time.": "Check the clock",
		"Title: Open Safari and search":             "Open Safari and search",
		"\n  “Tap the Speaker tile”  \n":            "Tap the Speaker tile",
		"":                                          "",
		strings.Repeat("word ", 30):                 "word word word word word word word word word word word word…",
		"Is the home screen showing?":               "Is the home screen showing?",
		"`vibium say` into the page":                "`vibium say` into the page",
	} {
		if got := tidyTitle(give); got != want {
			t.Errorf("%q: got %q, want %q", give, got, want)
		}
		if n := len([]rune(tidyTitle(give))); n > TitleLimit {
			t.Errorf("%q: a title of %d characters", give, n)
		}
	}
}

func TestTitle(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"\"Soundcheck microphone echo.\""},"finish_reason":"stop"}],"usage":{"prompt_tokens":90,"completion_tokens":6}}`))
	}))
	defer srv.Close()
	m := &Model{Provider: "openai", Name: "gpt-5.6-sol", BaseURL: srv.URL + "/v1", APIKey: "sk-secret"}

	// A prompt that is already a title is its own, and the model is not asked.
	title, usage, err := m.Title(context.Background(), "  Is the phone on its\nhome screen?  ")
	if err != nil || title != "Is the phone on its home screen?" || usage != (Usage{}) || calls != 0 {
		t.Fatalf("short prompt: %q, %+v, %v, %d calls", title, usage, err, calls)
	}

	long := "In Safari, open https://soundcheck.vibium.dev/. Tap Mic, and allow the microphone if the phone asks. Say \"Come here, Watson\" and tell me what you hear."
	title, usage, err = m.Title(context.Background(), long)
	if err != nil || title != "Soundcheck microphone echo" || usage.Prompt != 90 || usage.Completion != 6 || calls != 1 {
		t.Fatalf("long prompt: %q, %+v, %v, %d calls", title, usage, err, calls)
	}
	// One small request: the prompt, no tools, and no reasoning for a model that can do without.
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 2 || got["tools"] != nil || got["tool_choice"] != nil || got["reasoning_effort"] != "none" {
		t.Errorf("request: %v", got)
	}
	if last, _ := msgs[1].(map[string]any); last["role"] != "user" || last["content"] != long {
		t.Errorf("the prompt was not sent as it is: %v", msgs[1])
	}

	// A model that answers nothing leaves the jam without a title.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"  "},"finish_reason":"stop"}],"usage":{"prompt_tokens":90,"completion_tokens":0}}`))
	}))
	defer empty.Close()
	m.BaseURL = empty.URL + "/v1"
	if title, _, err := m.Title(context.Background(), long); err == nil || title != "" {
		t.Errorf("an empty answer: %q, %v", title, err)
	}
}
