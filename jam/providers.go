package jam

import "encoding/base64"

// Provider is one model provider as Vibium names it, with the base URL
// and the key variable its documentation gives, so the station's
// provider table is the same table.
type Provider struct {
	ID          string
	Label       string
	BaseURL     string // the default; "" means the model must name one
	KeyVar      string
	KeyRequired bool
	Effort      bool // takes a reasoning effort
}

// Providers is the table, in the order the Settings page offers them.
var Providers = []Provider{
	{ID: "openai", Label: "OpenAI", BaseURL: "https://api.openai.com/v1", KeyVar: "OPENAI_API_KEY", KeyRequired: true, Effort: true},
	{ID: "anthropic", Label: "Anthropic", BaseURL: "https://api.anthropic.com/v1", KeyVar: "ANTHROPIC_API_KEY", KeyRequired: true},
	{ID: "google", Label: "Google Gemini", BaseURL: "https://generativelanguage.googleapis.com/v1beta", KeyVar: "GOOGLE_API_KEY", KeyRequired: true},
	{ID: "xai", Label: "xAI", BaseURL: "https://api.x.ai/v1", KeyVar: "XAI_API_KEY", KeyRequired: true, Effort: true},
	{ID: "openai-compatible", Label: "OpenAI-compatible server", KeyVar: "OPENAI_API_KEY", Effort: true},
	{ID: "local", Label: "Local server", BaseURL: "http://127.0.0.1:8080/v1", KeyVar: "OPENAI_API_KEY", Effort: true},
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

// Efforts are the reasoning efforts Vibium accepts.
var Efforts = []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"}

func encodeBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
