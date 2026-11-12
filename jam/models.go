package jam

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Roles a model on Settings can have.
const (
	RoleAgent  = "agent"  // a jam's brain: reasons, sees screenshots, calls tools
	RoleListen = "listen" // hears the device, for Vibium's listen
	RoleSay    = "say"    // speaks into the device, for Vibium's say
)

// Roles in the order Settings offers them.
var Roles = []string{RoleAgent, RoleListen, RoleSay}

// ModelConfig is one model the owner set up: a name of their choosing,
// a role, and the provider settings in Vibium's own names.
type ModelConfig struct {
	Name            string
	Role            string
	Provider        string
	Model           string
	BaseURL         string
	ReasoningEffort string
	APIKey          string
	Default         bool
	Host            bool // from the station's environment; the page cannot edit it
}

// HasKey says whether a key is set.
func (c ModelConfig) HasKey() bool { return c.APIKey != "" }

// Client is the model as the loop talks to it.
func (c ModelConfig) Client() *Model {
	return &Model{Provider: c.Provider, Name: c.Model, BaseURL: c.BaseURL, APIKey: c.APIKey, ReasoningEffort: c.ReasoningEffort}
}

// Env is the model as Vibium reads it, for a subprocess that makes its
// own model calls: the provider and model under the names Vibium gives
// the role, and the provider's key variable. An agent's are
// VIBIUM_AI_*, a listen model's VIBIUM_LISTEN_* and a say model's
// VIBIUM_SAY_*.
func (c ModelConfig) Env() []string {
	prefix := "VIBIUM_AI_"
	switch c.Role {
	case RoleListen:
		prefix = "VIBIUM_LISTEN_"
	case RoleSay:
		prefix = "VIBIUM_SAY_"
	}
	if c.Provider == "piper-server" {
		url := c.BaseURL
		if url == "" {
			url = "http://127.0.0.1:5000"
		}
		return []string{"VIBIUM_SAY_PROVIDER=piper-server", "VIBIUM_SAY_VOICE=" + c.Model, "VIBIUM_PIPER_URL=" + url}
	}
	env := []string{prefix + "PROVIDER=" + c.Provider, prefix + "MODEL=" + c.Model}
	if prefix == "VIBIUM_AI_" {
		if c.BaseURL != "" {
			env = append(env, "VIBIUM_AI_BASE_URL="+c.BaseURL)
		}
		if c.ReasoningEffort != "" {
			env = append(env, "VIBIUM_AI_REASONING_EFFORT="+c.ReasoningEffort)
		}
	}
	if p, ok := ProviderByID(c.Provider); ok && c.APIKey != "" {
		env = append(env, p.KeyVar+"="+c.APIKey)
	}
	return env
}

// The station's own variables in a model's file, which Vibium's loader
// skips since it reads only the names it knows.
const (
	roleVar    = "STATION_AI_ROLE"
	defaultVar = "STATION_AI_DEFAULT"
)

// Store keeps the models as files in a directory, one per model in
// Vibium's ai.env format, so a file copied from a laptop works as it is.
type Store struct {
	Dir    string
	Getenv func(string) string // the station host's environment; nil for os.Getenv
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`)

// ErrNoModel says there is no model of that name.
var ErrNoModel = errors.New("no such model")

func (s *Store) getenv(k string) string {
	if s.Getenv != nil {
		return s.Getenv(k)
	}
	return os.Getenv(k)
}

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name+".env") }

// List is every model: the files by name, then the host's environment as
// a model named host when it carries Vibium's variables.
func (s *Store) List() ([]ModelConfig, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []ModelConfig
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".env")
		if e.IsDir() || name == e.Name() || !nameRe.MatchString(name) {
			continue
		}
		c, err := s.read(name)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if h, ok := s.host(); ok {
		out = append(out, h)
	}
	// One default per role: the one marked, else the only one, else the first.
	for _, role := range Roles {
		first, marked := -1, false
		for i := range out {
			if out[i].Role != role {
				continue
			}
			if first < 0 {
				first = i
			}
			if out[i].Default {
				if marked {
					out[i].Default = false
				}
				marked = true
			}
		}
		if first >= 0 && !marked {
			out[first].Default = true
		}
	}
	return out, nil
}

// host is the model the station's own environment describes, if any.
func (s *Store) host() (ModelConfig, bool) {
	provider := s.getenv("VIBIUM_AI_PROVIDER")
	if provider == "" {
		return ModelConfig{}, false
	}
	c := ModelConfig{Name: "host", Role: RoleAgent, Host: true, Provider: provider, Model: s.getenv("VIBIUM_AI_MODEL"),
		BaseURL: s.getenv("VIBIUM_AI_BASE_URL"), ReasoningEffort: s.getenv("VIBIUM_AI_REASONING_EFFORT")}
	if p, ok := ProviderByID(provider); ok {
		c.APIKey = s.getenv(p.KeyVar)
		if c.APIKey == "" && provider == "google" {
			c.APIKey = s.getenv("GEMINI_API_KEY")
		}
	}
	return c, true
}

// Get finds a model by name.
func (s *Store) Get(name string) (ModelConfig, error) {
	list, err := s.List()
	if err != nil {
		return ModelConfig{}, err
	}
	for _, c := range list {
		if c.Name == name {
			return c, nil
		}
	}
	return ModelConfig{}, ErrNoModel
}

// Default is the model of a role a jam uses when none is named.
func (s *Store) Default(role string) (ModelConfig, bool) {
	list, err := s.List()
	if err != nil {
		return ModelConfig{}, false
	}
	for _, c := range list {
		if c.Role == role && c.Default {
			return c, true
		}
	}
	return ModelConfig{}, false
}

// OfRole lists the models of one role.
func (s *Store) OfRole(role string) []ModelConfig {
	list, _ := s.List()
	var out []ModelConfig
	for _, c := range list {
		if c.Role == role {
			out = append(out, c)
		}
	}
	return out
}

// Validate checks a model's settings before they are saved.
func (c ModelConfig) Validate() error {
	if !nameRe.MatchString(c.Name) || c.Name == "host" {
		return errors.New("a model's name is letters, digits, dots, dashes or underscores, up to 40, and not host")
	}
	if c.Role != RoleAgent && c.Role != RoleListen && c.Role != RoleSay {
		return errors.New("the role is agent, listen or say")
	}
	p, ok := ProviderByID(c.Provider)
	if !ok {
		return errors.New("unknown provider")
	}
	if p.Role != "" && p.Role != c.Role {
		return fmt.Errorf("%s is for the %s role only", p.Label, p.Role)
	}
	if strings.TrimSpace(c.Model) == "" {
		if p.Role == RoleSay {
			return errors.New("the model is the voice's name, such as en_US-lessac-medium")
		}
		return errors.New("the model needs the provider's name for it")
	}
	if p.BaseURL == "" && c.BaseURL == "" {
		return errors.New("this provider needs a base URL")
	}
	if c.BaseURL != "" && !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return errors.New("the base URL starts with http:// or https://")
	}
	if c.ReasoningEffort != "" {
		found := false
		for _, e := range Efforts {
			found = found || e == c.ReasoningEffort
		}
		if !found {
			return errors.New("unknown reasoning effort")
		}
	}
	if p.KeyRequired && c.APIKey == "" {
		return fmt.Errorf("%s needs an API key", p.Label)
	}
	return nil
}

// Save writes a model's file. A model marked default unmarks the others
// of its role.
func (s *Store) Save(c ModelConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	if c.Default {
		if err := s.clearDefault(c.Role, c.Name); err != nil {
			return err
		}
	}
	return s.write(c)
}

func (s *Store) write(c ModelConfig) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: a model on the station's Settings, in Vibium's ai.env format.\n", c.Name)
	fmt.Fprintf(&b, "VIBIUM_AI_PROVIDER=%s\nVIBIUM_AI_MODEL=%s\n", c.Provider, c.Model)
	if c.BaseURL != "" {
		fmt.Fprintf(&b, "VIBIUM_AI_BASE_URL=%s\n", c.BaseURL)
	}
	if c.ReasoningEffort != "" {
		fmt.Fprintf(&b, "VIBIUM_AI_REASONING_EFFORT=%s\n", c.ReasoningEffort)
	}
	if p, ok := ProviderByID(c.Provider); ok && c.APIKey != "" {
		fmt.Fprintf(&b, "%s=%s\n", p.KeyVar, c.APIKey)
	}
	fmt.Fprintf(&b, "%s=%s\n", roleVar, c.Role)
	if c.Default {
		fmt.Fprintf(&b, "%s=1\n", defaultVar)
	}
	tmp := s.path(c.Name) + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(c.Name))
}

func (s *Store) clearDefault(role, except string) error {
	list, err := s.List()
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Role == role && c.Default && c.Name != except && !c.Host {
			c.Default = false
			if err := s.write(c); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetDefault makes a model its role's default.
func (s *Store) SetDefault(name string) error {
	c, err := s.Get(name)
	if err != nil {
		return err
	}
	if c.Host {
		return s.clearDefault(c.Role, name)
	}
	c.Default = true
	return s.Save(c)
}

// SetKey replaces a model's key.
func (s *Store) SetKey(name, key string) error {
	c, err := s.Get(name)
	if err != nil {
		return err
	}
	if c.Host {
		return errors.New("the host model is set in the station's environment")
	}
	c.APIKey = key
	return s.Save(c)
}

// Remove deletes a model's file.
func (s *Store) Remove(name string) error {
	if !nameRe.MatchString(name) || name == "host" {
		return ErrNoModel
	}
	if err := os.Remove(s.path(name)); errors.Is(err, os.ErrNotExist) {
		return ErrNoModel
	} else if err != nil {
		return err
	}
	return nil
}

// read parses one model's file. Lines are KEY=value, with optional
// export, quotes and comments, as a shell would write them.
func (s *Store) read(name string) (ModelConfig, error) {
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		return ModelConfig{}, err
	}
	vars := parseEnv(string(data))
	c := ModelConfig{Name: name, Role: vars[roleVar], Provider: vars["VIBIUM_AI_PROVIDER"], Model: vars["VIBIUM_AI_MODEL"],
		BaseURL: vars["VIBIUM_AI_BASE_URL"], ReasoningEffort: vars["VIBIUM_AI_REASONING_EFFORT"], Default: vars[defaultVar] == "1"}
	if c.Role == "" {
		c.Role = RoleAgent
	}
	if p, ok := ProviderByID(c.Provider); ok {
		c.APIKey = vars[p.KeyVar]
		if c.APIKey == "" && c.Provider == "google" {
			c.APIKey = vars["GEMINI_API_KEY"]
		}
		// A file without a key leans on the host's environment.
		if c.APIKey == "" {
			c.APIKey = s.getenv(p.KeyVar)
		}
	}
	return c, nil
}

// parseEnv reads KEY=value lines.
func parseEnv(text string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		vars[k] = v
	}
	return vars
}
