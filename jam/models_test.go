package jam

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{}
	s := &Store{Dir: dir, Getenv: func(k string) string { return env[k] }}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %v, %v", list, err)
	}
	if err := s.Save(ModelConfig{Name: "gpt", Role: RoleAgent, Provider: "openai", Model: "gpt-5", APIKey: "sk-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ModelConfig{Name: "claude", Role: RoleAgent, Provider: "anthropic", Model: "claude-sonnet-4-6", APIKey: "sk-ant"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ModelConfig{Name: "ears", Role: RoleListen, Provider: "openai", Model: "whisper-1", APIKey: "sk-1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(ModelConfig{Name: "bad", Role: RoleAgent, Provider: "openai", Model: "gpt-5"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("a key is required for OpenAI: %v", err)
	}
	if err := s.Save(ModelConfig{Name: "host", Role: RoleAgent, Provider: "local", Model: "x"}); err == nil {
		t.Error("host is reserved")
	}
	info, err := os.Stat(filepath.Join(dir, "gpt.env"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v, %v", info, err)
	}
	// With nothing marked, the first of a role is the default.
	d, ok := s.Default(RoleAgent)
	if !ok || d.Name != "claude" {
		t.Errorf("default agent: %+v, %v", d, ok)
	}
	if err := s.SetDefault("gpt"); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Default(RoleAgent)
	if d.Name != "gpt" {
		t.Errorf("default after set: %s", d.Name)
	}
	if c, _ := s.Get("claude"); c.Default {
		t.Error("the other agent should have lost the default mark")
	}
	if l, ok := s.Default(RoleListen); !ok || l.Name != "ears" || len(s.OfRole(RoleAgent)) != 2 {
		t.Errorf("roles: %+v %v", l, ok)
	}
	if err := s.SetKey("gpt", "sk-2"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get("gpt"); c.APIKey != "sk-2" || !c.Default || strings.Join(c.Env(), " ") != "VIBIUM_AI_PROVIDER=openai VIBIUM_AI_MODEL=gpt-5 OPENAI_API_KEY=sk-2" {
		t.Errorf("after key change: %+v %v", c, c.Env())
	}
	// A file copied from a laptop, with export lines and quotes and no role.
	os.WriteFile(filepath.Join(dir, "laptop.env"), []byte("export VIBIUM_AI_PROVIDER=\"xai\"\nVIBIUM_AI_MODEL='grok-4'\n# a comment\nXAI_API_KEY=xai-1\n"), 0o600)
	if c, err := s.Get("laptop"); err != nil || c.Role != RoleAgent || c.Provider != "xai" || c.Model != "grok-4" || c.APIKey != "xai-1" {
		t.Errorf("laptop file: %+v, %v", c, err)
	}
	// The host's environment is one more agent model, and the default
	// only when no file is marked.
	env["VIBIUM_AI_PROVIDER"], env["VIBIUM_AI_MODEL"], env["GEMINI_API_KEY"] = "google", "gemini-2.5-flash", "g-1"
	h, err := s.Get("host")
	if err != nil || !h.Host || h.APIKey != "g-1" || h.Default {
		t.Errorf("host model: %+v, %v", h, err)
	}
	if err := s.SetKey("host", "x"); err == nil {
		t.Error("the host model cannot be edited")
	}
	if err := s.Remove("gpt"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove("gpt"); err != ErrNoModel {
		t.Errorf("removing twice: %v", err)
	}
	if _, err := s.Get("gpt"); err != ErrNoModel {
		t.Errorf("removed model found: %v", err)
	}
}

// A listen or say model reaches Vibium under the names Vibium reads for
// that role, so a jam's ears and voice never borrow the agent's settings.
func TestEnvNamesFollowTheRole(t *testing.T) {
	agent := ModelConfig{Role: RoleAgent, Provider: "openai", Model: "gpt-5.6-sol", ReasoningEffort: "none", APIKey: "sk-a"}
	if got := strings.Join(agent.Env(), " "); got != "VIBIUM_AI_PROVIDER=openai VIBIUM_AI_MODEL=gpt-5.6-sol VIBIUM_AI_REASONING_EFFORT=none OPENAI_API_KEY=sk-a" {
		t.Errorf("agent: %s", got)
	}
	listen := ModelConfig{Role: RoleListen, Provider: "openai", Model: "gpt-transcribe", ReasoningEffort: "none", BaseURL: "https://example.test/v1", APIKey: "sk-l"}
	if got := strings.Join(listen.Env(), " "); got != "VIBIUM_LISTEN_PROVIDER=openai VIBIUM_LISTEN_MODEL=gpt-transcribe OPENAI_API_KEY=sk-l" {
		t.Errorf("listen: %s", got)
	}
	say := ModelConfig{Role: RoleSay, Provider: "openai", Model: "gpt-4o-mini-tts", APIKey: "sk-s"}
	if got := strings.Join(say.Env(), " "); got != "VIBIUM_SAY_PROVIDER=openai VIBIUM_SAY_MODEL=gpt-4o-mini-tts OPENAI_API_KEY=sk-s" {
		t.Errorf("say: %s", got)
	}
}

// A Piper server speaks with the voice named as the model, at the base
// URL or Piper's own default address, and only in the say role.
func TestPiperServerModel(t *testing.T) {
	piper := ModelConfig{Name: "danny", Role: RoleSay, Provider: "piper-server", Model: "en_US-danny-low", BaseURL: "http://127.0.0.1:5005"}
	if err := piper.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(piper.Env(), " "); got != "VIBIUM_SAY_PROVIDER=piper-server VIBIUM_SAY_VOICE=en_US-danny-low VIBIUM_PIPER_URL=http://127.0.0.1:5005" {
		t.Errorf("env: %s", got)
	}
	// With no address it is the station's own Piper, whose address the
	// station adds when a jam starts.
	piper.BaseURL = ""
	if got := strings.Join(piper.Env(), " "); got != "VIBIUM_SAY_PROVIDER=piper-server VIBIUM_SAY_VOICE=en_US-danny-low" || !piper.OwnPiper() {
		t.Errorf("env with no base URL: %s", got)
	}
	if err := piper.Validate(); err != nil {
		t.Errorf("the station's own Piper: %v", err)
	}
	for _, role := range []string{RoleAgent, RoleListen} {
		c := piper
		c.Role = role
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be a "+role+" model") {
			t.Errorf("as %s: %v", role, err)
		}
	}
	piper.Model = ""
	if err := piper.Validate(); err == nil || !strings.Contains(err.Error(), "voice") {
		t.Errorf("with no voice: %v", err)
	}
	// Saved and read back, it is the same model.
	s := &Store{Dir: t.TempDir(), Getenv: func(string) string { return "" }}
	piper.Model, piper.BaseURL = "en_US-danny-low", "http://127.0.0.1:5005"
	if err := s.Save(piper); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("danny")
	if err != nil || got.Provider != "piper-server" || got.Model != "en_US-danny-low" || got.BaseURL != "http://127.0.0.1:5005" || got.Role != RoleSay {
		t.Errorf("read back %+v %v", got, err)
	}
}

// The station's own Piper is listed without a file, is the say default
// unless another is marked, and becomes a file once it is changed.
func TestBuiltinPiper(t *testing.T) {
	builtin := ModelConfig{Name: "piper", Role: RoleSay, Provider: "piper-server", Model: "en_US-danny-low"}
	s := &Store{Dir: t.TempDir(), Getenv: func(string) string { return "" }, Builtin: func() []ModelConfig { return []ModelConfig{builtin} }}
	if err := s.Save(ModelConfig{Name: "tts", Role: RoleSay, Provider: "openai", Model: "gpt-4o-mini-tts", APIKey: "sk-s"}); err != nil {
		t.Fatal(err)
	}
	c, ok := s.Default(RoleSay)
	if !ok || c.Name != "piper" || !c.Builtin {
		t.Fatalf("the say default is %+v", c)
	}
	// Marking the hosted one wins over the local one.
	if err := s.SetDefault("tts"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Default(RoleSay); c.Name != "tts" {
		t.Errorf("after marking tts: %s", c.Name)
	}
	// Choosing Piper again writes its file, which then stands in for it.
	if err := s.SetDefault("piper"); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Default(RoleSay)
	if c.Name != "piper" || c.Builtin || c.Model != "en_US-danny-low" || !c.OwnPiper() {
		t.Errorf("after marking piper: %+v", c)
	}
	if n := len(s.OfRole(RoleSay)); n != 2 {
		t.Errorf("%d say models, want 2", n)
	}
	// Without the station's Piper there is nothing to list.
	s.Builtin = nil
	if err := s.Remove("piper"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Default(RoleSay); c.Name != "tts" {
		t.Errorf("with no Piper: %s", c.Name)
	}
}

// Each role offers the providers Vibium can use in it.
func TestProvidersFor(t *testing.T) {
	ids := func(ps []Provider) string {
		var out []string
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return strings.Join(out, " ")
	}
	if got := ids(ProvidersFor(RoleAgent)); got != "openai anthropic google xai openai-compatible local" {
		t.Errorf("agent: %s", got)
	}
	if got := ids(ProvidersFor(RoleSay)); got != "openai piper-server" {
		t.Errorf("say: %s", got)
	}
	if got := ids(ProvidersFor(RoleListen)); got != "openai" {
		t.Errorf("listen: %s", got)
	}
	if err := (ModelConfig{Name: "c", Role: RoleSay, Provider: "anthropic", Model: "m", APIKey: "k"}).Validate(); err == nil {
		t.Error("an Anthropic say model was taken")
	}
}
