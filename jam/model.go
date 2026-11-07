package jam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Model is one chat model behind an OpenAI-compatible endpoint: chat
// completions with tool calls and image inputs. That one protocol
// reaches OpenAI, xAI, local servers and any compatible endpoint
// directly, and Google and Anthropic through the compatible endpoints
// each serves.
type Model struct {
	Provider        string // openai, xai, anthropic, google, openai-compatible, local
	Name            string // the provider's name for the model
	BaseURL         string // "" for the provider's default
	APIKey          string
	ReasoningEffort string
	HTTP            *http.Client // nil for a client with a three-minute timeout
}

// Message is one turn of the conversation, in the chat-completions shape.
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content,omitempty"` // a string, or []Part for text with images
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Part is one piece of a user message: {"type":"text","text":…} or
// {"type":"image_url","image_url":{"url":"data:…"}}.
type Part map[string]any

// TextPart and ImagePart make the two kinds of part.
func TextPart(text string) Part { return Part{"type": "text", "text": text} }
func ImagePart(mime string, data []byte) Part {
	return Part{"type": "image_url", "image_url": map[string]string{"url": "data:" + mime + ";base64," + encodeBase64(data)}}
}

// ToolCall is the model asking for a tool.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// Tool is a function the model may call.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Usage is what one request cost.
type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
}

// Reply is the model's answer to one request.
type Reply struct {
	Message      Message
	FinishReason string
	Usage        Usage
}

// MaxOutputTokens bounds one answer.
const MaxOutputTokens = 4096

// Endpoint is the base URL the model is reached at: the one set, or the
// provider's default, with the compatible path Google serves its
// OpenAI-shaped API under.
func (m *Model) Endpoint() string {
	base := strings.TrimSuffix(m.BaseURL, "/")
	if base == "" {
		if p, ok := ProviderByID(m.Provider); ok {
			base = p.BaseURL
		}
	}
	if m.Provider == "google" && !strings.HasSuffix(base, "/openai") {
		base += "/openai"
	}
	return base
}

// Complete asks the model for its next message given the conversation
// and the tools; force names a tool the model must call. An OpenAI
// model with no effort set gets the one its family needs for tools,
// and a provider that refuses tools with reasoning is asked once more
// with reasoning off.
func (m *Model) Complete(ctx context.Context, messages []Message, tools []Tool, force string) (Reply, error) {
	effort := m.ReasoningEffort
	if effort == "" && m.Provider == "openai" && len(tools) > 0 {
		effort = OpenAIEffortDefault(m.Name)
	}
	reply, err := m.complete(ctx, messages, tools, force, effort)
	if err != nil && effort != "none" && len(tools) > 0 && strings.Contains(err.Error(), "reasoning_effort") {
		reply, err = m.complete(ctx, messages, tools, force, "none")
	}
	return reply, err
}

func (m *Model) complete(ctx context.Context, messages []Message, tools []Tool, force, effort string) (Reply, error) {
	payload := map[string]any{"model": m.Name, "messages": messages}
	if len(tools) > 0 {
		var fs []map[string]any
		for _, t := range tools {
			fs = append(fs, map[string]any{"type": "function", "function": t})
		}
		payload["tools"] = fs
	}
	if m.Provider == "openai" {
		payload["max_completion_tokens"] = MaxOutputTokens
	} else {
		payload["max_tokens"] = MaxOutputTokens
	}
	if force != "" {
		payload["tool_choice"] = map[string]any{"type": "function", "function": map[string]string{"name": force}}
		if m.Provider == "openai" || m.Provider == "xai" {
			payload["parallel_tool_calls"] = false
		}
	}
	if effort != "" && m.Provider != "anthropic" && m.Provider != "google" {
		payload["reasoning_effort"] = effort
	}
	data, err := m.post(ctx, m.Endpoint()+"/chat/completions", payload)
	if err != nil {
		return Reply{}, err
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Role      string          `json:"role"`
				Content   json.RawMessage `json:"content"`
				ToolCalls []ToolCall      `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &completion); err != nil || len(completion.Choices) == 0 {
		return Reply{}, errors.New("the model's answer was not a chat completion")
	}
	c := completion.Choices[0]
	reply := Reply{FinishReason: c.FinishReason, Usage: completion.Usage}
	reply.Message = Message{Role: "assistant", ToolCalls: c.Message.ToolCalls, Content: contentText(c.Message.Content)}
	return reply, nil
}

// contentText reads a message's content, which a provider may send as a
// string, null, or a list of text parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// Ping sends the smallest request that proves the model answers the way
// a jam needs, with a tool to call, which is the readiness check
// Settings runs when a model is saved, and returns what it said.
func (m *Model) Ping(ctx context.Context) (string, error) {
	if problem := ChatToolsProblem(m.Provider, m.Name); problem != "" {
		return "", errors.New(problem)
	}
	ready := Tool{Name: "ready", Description: "Say that you are ready.", Parameters: json.RawMessage(`{"type":"object","properties":{"word":{"type":"string","description":"One word."}},"required":["word"]}`)}
	reply, err := m.Complete(ctx, []Message{{Role: "user", Content: "Call the ready tool with the word ready."}}, []Tool{ready}, ready.Name)
	if err != nil {
		return "", err
	}
	for _, c := range reply.Message.ToolCalls {
		if c.Function.Name == ready.Name {
			var args struct{ Word string }
			json.Unmarshal([]byte(c.Function.Arguments), &args)
			if args.Word != "" {
				return args.Word, nil
			}
			return "ready", nil
		}
	}
	text, _ := reply.Message.Content.(string)
	if text == "" {
		return "", errors.New("the model answered without calling the tool")
	}
	return strings.TrimSpace(text), nil
}

func (m *Model) post(ctx context.Context, url string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.Provider, redact(err.Error(), m.APIKey))
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s answered HTTP %d%s", m.Provider, res.StatusCode, errorDetail(data, m.APIKey))
	}
	return data, nil
}

// errorDetail is the provider's own error message, short and with the
// key struck out should it echo one, for the page and the transcript.
func errorDetail(data []byte, key string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	msg := ""
	if json.Unmarshal(data, &e) == nil {
		msg = e.Error.Message
	}
	if msg == "" {
		msg = strings.TrimSpace(string(data))
	}
	msg = redact(msg, key).Error()
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		return ""
	}
	return ": " + msg
}

// redact strikes the key out of a message that may echo it.
func redact(msg, key string) error {
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[key]")
	}
	return errors.New(msg)
}
