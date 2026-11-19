// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aiendpoint

// PresetCustom is the name of the preset with no default value.
const PresetCustom = "custom"

// Preset is one row of the table of Spec 093, "Provider presets". A preset sets
// the defaults of a new endpoint. A preset with two protocols has two rows.
type Preset struct {
	// Name is the identifier of the preset, such as "openrouter".
	Name string
	// Label is the display name of the preset.
	Label    string
	Protocol Protocol
	// BaseURL is the default base URL. It is empty when the administrator must
	// give one.
	BaseURL string
	// Auth is the default authentication form.
	Auth Auth
	// ModelsURL is the URL of the list of models. The text {base_url} stands for
	// the base URL of the endpoint. It is empty when the preset gives none.
	ModelsURL string
	// ChatType is the provider type that chat.completion uses for the row.
	ChatType string
	// Local tells that the preset is a local model. It accepts the scheme http and
	// an endpoint with no key.
	Local bool
}

// presets is the table of Spec 093. The values come from the documentation of
// each provider. The spec lists the values that are still to verify.
var presets = []Preset{
	{"anthropic", "Anthropic", ProtocolAnthropic, "https://api.anthropic.com", AuthXAPIKey, "https://api.anthropic.com/v1/models", "anthropic", false},
	{"openai", "OpenAI", ProtocolOpenAI, "https://api.openai.com/v1", AuthBearer, "https://api.openai.com/v1/models", "openai", false},
	{"google-gemini", "Google Gemini", ProtocolOpenAI, "https://generativelanguage.googleapis.com/v1beta/openai", AuthBearer, "https://generativelanguage.googleapis.com/v1beta/openai/models", "openai", false},
	{"mistral", "Mistral", ProtocolOpenAI, "https://api.mistral.ai/v1", AuthBearer, "https://api.mistral.ai/v1/models", "openai", false},
	{"deepseek", "DeepSeek", ProtocolOpenAI, "https://api.deepseek.com", AuthBearer, "https://api.deepseek.com/models", "openai", false},
	{"deepseek", "DeepSeek", ProtocolAnthropic, "https://api.deepseek.com/anthropic", AuthXAPIKey, "https://api.deepseek.com/models", "anthropic", false},
	{"xai", "xAI (Grok)", ProtocolOpenAI, "https://api.x.ai/v1", AuthBearer, "https://api.x.ai/v1/models", "openai", false},
	{"groq", "Groq", ProtocolOpenAI, "https://api.groq.com/openai/v1", AuthBearer, "https://api.groq.com/openai/v1/models", "openai", false},
	{"openrouter", "OpenRouter", ProtocolOpenAI, "https://openrouter.ai/api/v1", AuthBearer, "https://openrouter.ai/api/v1/models", "openrouter", false},
	{"openrouter", "OpenRouter", ProtocolAnthropic, "https://openrouter.ai/api", AuthBearer, "https://openrouter.ai/api/v1/models", "anthropic", false},
	{"zai", "Z.ai", ProtocolOpenAI, "https://api.z.ai/api/paas/v4", AuthBearer, "", "zai", false},
	{"zai", "Z.ai", ProtocolAnthropic, "https://api.z.ai/api/anthropic", AuthBearer, "", "anthropic", false},
	{"together", "Together AI", ProtocolOpenAI, "https://api.together.ai/v1", AuthBearer, "https://api.together.ai/v1/models", "openai", false},
	{"ollama", "Local model (Ollama)", ProtocolOpenAI, "http://localhost:11434/v1", AuthBearer, "http://localhost:11434/v1/models", "local", true},
	{"ollama", "Local model (Ollama)", ProtocolAnthropic, "http://localhost:11434", AuthBearer, "http://localhost:11434/v1/models", "anthropic", true},
	{"vllm", "Local model (vLLM)", ProtocolOpenAI, "", AuthBearer, "{base_url}/models", "local", true},
	{"vllm", "Local model (vLLM)", ProtocolAnthropic, "", AuthXAPIKey, "{base_url}/v1/models", "anthropic", true},
	{"litellm", "Local model (LiteLLM)", ProtocolOpenAI, "", AuthBearer, "{base_url}/models", "local", true},
	{"litellm", "Local model (LiteLLM)", ProtocolAnthropic, "", AuthXAPIKey, "", "anthropic", true},
}

// Presets returns the rows of the table, in the order of the spec.
func Presets() []Preset {
	return append([]Preset(nil), presets...)
}

// LookupPreset returns the row of a preset for a protocol. The preset custom has
// a row for each protocol. It has no default value, and it accepts the scheme
// http. LookupPreset returns false when the preset has no row for the protocol.
func LookupPreset(name string, protocol Protocol) (Preset, bool) {
	if protocol != ProtocolAnthropic && protocol != ProtocolOpenAI {
		return Preset{}, false
	}
	if name == PresetCustom {
		return Preset{Name: PresetCustom, Label: "Custom", Protocol: protocol, ChatType: string(protocol)}, true
	}
	for _, p := range presets {
		if p.Name == name && p.Protocol == protocol {
			return p, true
		}
	}
	return Preset{}, false
}

// presetKnown tells if the table has a preset of this name, or if it is custom.
func presetKnown(name string) bool {
	if name == PresetCustom {
		return true
	}
	for _, p := range presets {
		if p.Name == name {
			return true
		}
	}
	return false
}

// AllowsNoKey tells if an endpoint of the preset may have no key.
func (p Preset) AllowsNoKey() bool { return p.Local }

// allowsHTTP tells if the base URL of the preset may use the scheme http.
func (p Preset) allowsHTTP() bool { return p.Local || p.Name == PresetCustom }

// Consumer names a client that can use a row of the table. The values are the
// column "Consumers" of Spec 093.
const (
	// ConsumerClaude is the CLI claude. It needs the protocol anthropic.
	ConsumerClaude = "claude"
	// ConsumerCodex is the CLI codex. It needs the protocol openai. agent.run
	// does not run it yet.
	ConsumerCodex = "codex"
	// ConsumerChatCompletion is the action chat.completion.
	ConsumerChatCompletion = "chat.completion"
)

// Consumers returns the clients that can use the row. The native Gemini API is
// not a protocol, so the row of Google Gemini serves chat.completion only.
func (p Preset) Consumers() []string {
	if p.Protocol == ProtocolAnthropic {
		return []string{ConsumerClaude}
	}
	if p.Name == "google-gemini" {
		return []string{ConsumerChatCompletion}
	}
	return []string{ConsumerCodex, ConsumerChatCompletion}
}

// CustomPresets returns the two rows of the preset custom, which has no default
// value.
func CustomPresets() []Preset {
	var rows []Preset
	for _, protocol := range []Protocol{ProtocolAnthropic, ProtocolOpenAI} {
		row, _ := LookupPreset(PresetCustom, protocol)
		rows = append(rows, row)
	}
	return rows
}
