package jam

import (
	"encoding/base64"
	"strings"
)

// Provider is one model provider as Vibium names it, with the base URL
// and the key variable its documentation gives, so the station's
// provider table is the same table.
type Provider struct {
	ID          string
	Label       string
	BaseURL     string // the default; "" means the model must name one
	KeyVar      string
	KeyRequired bool
	Effort      bool     // takes a reasoning effort
	Roles       []string // the roles Vibium can use it in
}

// For says whether a provider can have a role.
func (p Provider) For(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// ProvidersFor is the table for one role, in the same order.
func ProvidersFor(role string) []Provider {
	var out []Provider
	for _, p := range Providers {
		if p.For(role) {
			out = append(out, p)
		}
	}
	return out
}

// Roles a provider of chat models has: Vibium speaks and listens only
// through OpenAI among them.
var (
	agentOnly = []string{RoleAgent}
	allRoles  = []string{RoleAgent, RoleListen, RoleSay}
)

// Providers is the table, in the order the Settings page offers them.
var Providers = []Provider{
	{ID: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", KeyVar: "OPENAI_API_KEY", KeyRequired: true, Effort: true, Roles: allRoles},
	{ID: "anthropic", Label: "Anthropic", BaseURL: "https://api.anthropic.com/v1", KeyVar: "ANTHROPIC_API_KEY", KeyRequired: true, Roles: agentOnly},
	{ID: "google", Label: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta", KeyVar: "GOOGLE_API_KEY", KeyRequired: true, Roles: agentOnly},
	{ID: "xai", Label: "xAI", BaseURL: "https://api.x.ai/v1", KeyVar: "XAI_API_KEY", KeyRequired: true, Effort: true, Roles: agentOnly},
	{ID: "openai-compatible", Label: "OpenAI-compatible server", KeyVar: "OPENAI_API_KEY", Effort: true, Roles: agentOnly},
	{ID: "local", Label: "Local server", BaseURL: "http://127.0.0.1:8080/v1", KeyVar: "OPENAI_API_KEY", Effort: true, Roles: agentOnly},
	// A Piper server, which keeps a voice loaded. The model is the voice's
	// name; Vibium sends it text and gets a WAV. With no base URL it is
	// the server the station runs itself.
	{ID: "piper-server", Label: "Piper server", Roles: []string{RoleSay}},
	// A sherpa-onnx streaming server, which keeps a speech-to-text model
	// loaded. Vibium sends it audio over a WebSocket and gets the words.
	// With no base URL it is the server the station runs itself, and the
	// model is the name of the model's folder.
	{ID: "sherpa-server", Label: "sherpa-onnx server", Roles: []string{RoleListen}},
}

// ProviderByID finds a provider.
func ProviderByID(id string) (Provider, bool) {
	for _, p := range Providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// openAIEfforts maps OpenAI model families to the reasoning effort they
// need for function tools on chat completions, as Vibium's own table
// has it: the families marked refuse function tools there at every
// effort and need OpenAI's Responses API, which the loop does not speak.
var openAIEfforts = []struct {
	prefix      string
	effort      string
	noChatTools bool
}{
	{prefix: "gpt-6.1", noChatTools: true},
	{prefix: "gpt-6", effort: "none"},
	{prefix: "gpt-5", effort: "none"},
}

// OpenAIEffortDefault is the reasoning effort an OpenAI model needs for
// tool calls on chat completions when none is set, or "".
func OpenAIEffortDefault(model string) string {
	for _, m := range openAIEfforts {
		if strings.HasPrefix(strings.TrimSpace(model), m.prefix) {
			return m.effort
		}
	}
	return ""
}

// ChatToolsProblem says why a model cannot drive a jam, or "".
func ChatToolsProblem(provider, model string) string {
	if provider != "openai" {
		return ""
	}
	for _, m := range openAIEfforts {
		if strings.HasPrefix(strings.TrimSpace(model), m.prefix) && m.noChatTools {
			return model + " takes function tools only on OpenAI's Responses API, which jams do not speak yet; choose another model, such as gpt-5.6-sol"
		}
	}
	return ""
}

// Efforts are the reasoning efforts Vibium accepts.
var Efforts = []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
