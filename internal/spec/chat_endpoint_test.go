// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 094, "chat.completion". These tests load a DAG with one chat.completion
// step. The loader reads no store, so it cannot tell if an endpoint exists.

func loadChatStep(t *testing.T, with string) (*ir.DAG, error) {
	t.Helper()
	return spec.LoadYAML(t.Context(), []byte("steps:\n  - id: s\n    action: chat.completion\n    with:\n"+with+"\n"),
		spec.WithDAGsDir(t.TempDir()))
}

// The key endpoint of a chat.completion step is a block with the keys name,
// model and effort. The loader compiles it to the same form as the old keys.
func TestChatCompletionEndpoint(t *testing.T) {
	t.Parallel()

	dag, err := loadChatStep(t, "      endpoint:\n        name: cle1\n        model: claude-haiku-4-5\n      prompt: hello")
	require.NoError(t, err)
	step := dag.Steps[0]
	require.NotNil(t, step.LLM)
	assert.Equal(t, "cle1", step.LLM.Endpoint)
	assert.Empty(t, step.LLM.Provider, "the endpoint gives the provider type")
	models := step.LLM.GetModels()
	require.Len(t, models, 1)
	assert.Equal(t, "cle1", models[0].Endpoint)
	assert.Equal(t, "claude-haiku-4-5", step.LLM.Model)
	assert.Nil(t, step.LLM.Thinking, "a block with no effort sets no thinking")
	assert.Equal(t, []string{"cle1"}, step.AIEndpoints())
	assert.Equal(t, []string{ir.CapabilityAIRelay}, dag.RequiredCapabilities(),
		"a worker needs the capability of the relay for a chat step with an endpoint")
}

// The effort of the block compiles to the thinking configuration of the old keys:
// enabled, with this effort.
func TestChatCompletionEndpointEffort(t *testing.T) {
	t.Parallel()

	for _, effort := range ir.AgentEffortLevels {
		dag, err := loadChatStep(t, "      endpoint: { name: cle1, model: m, effort: "+effort+" }\n      prompt: hello")
		require.NoError(t, err, effort)
		thinking := dag.Steps[0].LLM.Thinking
		require.NotNil(t, thinking, effort)
		assert.True(t, thinking.Enabled, effort)
		assert.Equal(t, ir.ThinkingEffort(effort), thinking.Effort)
	}

	block, err := loadChatStep(t, "      endpoint: { name: cle1, model: m, effort: high }\n      prompt: hello")
	require.NoError(t, err)
	old, err := loadChatStep(t, "      provider: openai\n      model: m\n      thinking: { enabled: true, effort: high }\n      prompt: hello")
	require.NoError(t, err)
	assert.Equal(t, old.Steps[0].LLM.Thinking, block.Steps[0].LLM.Thinking, "the same as the old thinking block")
}

// The model of the block can be a reference. The effort is a literal, because the
// runtime does not resolve a reference in the thinking configuration.
func TestChatCompletionEndpointModelReference(t *testing.T) {
	t.Parallel()

	dag, err := loadChatStep(t, "      endpoint: { name: cle1, model: '${MODEL}' }\n      prompt: hello")
	require.NoError(t, err)
	assert.Equal(t, "${MODEL}", dag.Steps[0].LLM.Model)
	assert.Equal(t, "${MODEL}", dag.Steps[0].LLM.GetModels()[0].Name)
}

// A step with no endpoint is the old method. It needs no capability, and the key
// thinking stays valid.
func TestChatCompletionOldMethodNeedsNoEndpoint(t *testing.T) {
	t.Parallel()

	dag, err := loadChatStep(t, "      provider: openai\n      api_key_name: MY_KEY\n      base_url: https://example.com/v1\n      model: gpt-4o\n      thinking: { enabled: true, effort: low }\n      prompt: hello")
	require.NoError(t, err)
	step := dag.Steps[0]
	assert.Empty(t, step.LLM.Endpoint)
	assert.Equal(t, "MY_KEY", step.LLM.APIKeyName)
	assert.Equal(t, "https://example.com/v1", step.LLM.BaseURL)
	assert.Equal(t, "gpt-4o", step.LLM.Model)
	require.NotNil(t, step.LLM.Thinking)
	assert.Equal(t, ir.ThinkingEffortLow, step.LLM.Thinking.Effort)
	assert.Nil(t, step.AIEndpoints())
	assert.Empty(t, dag.RequiredCapabilities())
}

// A step that needs a fallback keeps the list form of the key model. Each entry
// names its own endpoint, or keeps the old method with provider.
func TestChatCompletionEndpointOfModelEntries(t *testing.T) {
	t.Parallel()

	dag, err := loadChatStep(t, `      model:
        - name: claude-sonnet-4-6
          endpoint: cle1
        - name: gpt-4o
          provider: openai
          api_key_name: OPENAI_API_KEY
        - name: gemini-2.5-pro
          endpoint: cle2
        - name: local-model
          base_url: http://localhost:8080
          provider: local
      prompt: hello`)
	require.NoError(t, err)
	assert.Empty(t, dag.Steps[0].LLM.Endpoint, "a list has no endpoint at the level of the step")
	models := dag.Steps[0].LLM.GetModels()
	require.Len(t, models, 4)
	assert.Equal(t, "cle1", models[0].Endpoint)
	assert.Empty(t, models[1].Endpoint, "an entry with provider keeps the old method")
	assert.Equal(t, "openai", models[1].Provider)
	assert.Equal(t, "cle2", models[2].Endpoint, "the endpoint of the entry")
	assert.Empty(t, models[3].Endpoint)
	assert.Equal(t, []string{"cle1", "cle2"}, dag.Steps[0].AIEndpoints())
}

// An entry of the list that names an endpoint needs no provider. An entry with no
// provider and no endpoint is an error.
func TestChatCompletionModelEntryNeedsProviderOrEndpoint(t *testing.T) {
	t.Parallel()

	_, err := loadChatStep(t, "      model:\n        - name: m1\n          endpoint: cle1\n        - name: m2\n          endpoint: cle2\n      prompt: hi")
	require.NoError(t, err)

	_, err = loadChatStep(t, "      model:\n        - name: m1\n      prompt: hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider: required")

	_, err = loadChatStep(t, "      model:\n        - name: m1\n          endpoint: cle1\n        - name: m2\n      prompt: hi")
	require.Error(t, err, "an entry with no endpoint and no provider is an error")
	assert.Contains(t, err.Error(), "model[1].provider: required")

	_, err = loadChatStep(t, "      model:\n        - name: m1\n          base_url: http://localhost:8080\n      prompt: hi")
	require.Error(t, err, "an entry with base_url keeps the old method, which needs a provider")
	assert.Contains(t, err.Error(), "provider: required")
}

// The key endpoint of the with is a block. A value of another type is an error
// that names the type of the key.
func TestChatCompletionStepEndpointMustBeABlock(t *testing.T) {
	t.Parallel()

	for _, with := range []string{
		"endpoint: cle1\n      model: m\n      prompt: hi",
		"endpoint: cle1\n      model:\n        - name: m1\n        - name: m2\n          endpoint: cle2\n      prompt: hi",
		"endpoint: ''\n      model: m\n      prompt: hi",
		"endpoint: [cle1]\n      model: m\n      prompt: hi",
		"endpoint: 3\n      model: m\n      prompt: hi",
		"endpoint: null\n      model: m\n      prompt: hi",
		"endpoint: ${ENDPOINT}\n      model: m\n      prompt: hi",
	} {
		_, err := loadChatStep(t, "      "+with)
		require.Error(t, err, with)
		assert.Contains(t, err.Error(), "field 'with.endpoint'", with)
		assert.Contains(t, err.Error(), "endpoint must be a block with name and model", with)
	}
}

// A key of the block that is missing, of another type or unknown is an error that
// names the path of the key.
func TestChatCompletionEndpointBlockErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"empty block", "endpoint: {}", []string{"with.endpoint.name", "chat.completion needs the name of an endpoint", "with.endpoint.model", "chat.completion needs a model; choose one that the endpoint lists"}},
		{"no name", "endpoint: { model: m }", []string{"with.endpoint.name", "chat.completion needs the name of an endpoint"}},
		{"no model", "endpoint: { name: cle1 }", []string{"with.endpoint.model", "chat.completion needs a model; choose one that the endpoint lists"}},
		{"effort without a model", "endpoint: { name: cle1, effort: high }", []string{"with.endpoint.model", "chat.completion needs a model"}},
		{"unknown key", "endpoint: { name: cle1, model: m, temperature: 1 }", []string{"with.endpoint.temperature", `unknown key "temperature" in the block endpoint; valid keys are name, model, effort`}},
		{"name number", "endpoint: { name: 3, model: m }", []string{"with.endpoint.name", "name must be a non-empty string"}},
		{"name empty", "endpoint: { name: '', model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"name list", "endpoint: { name: [cle1], model: m }", []string{"with.endpoint.name", "non-empty string"}},
		{"name upper case", "endpoint: { name: Cle1, model: m }", []string{"with.endpoint.name", "name must match ^[a-z][a-z0-9-]{0,63}$"}},
		{"name slash", "endpoint: { name: a/b, model: m }", []string{"with.endpoint.name", "must match"}},
		{"name too long", "endpoint: { name: a" + strings.Repeat("b", 64) + ", model: m }", []string{"with.endpoint.name", "must match"}},
		{"name braced reference", "endpoint: { name: '${ENDPOINT}', model: m }", []string{"with.endpoint.name", "literal name, not a reference"}},
		{"name plain reference", "endpoint: { name: '$ENDPOINT', model: m }", []string{"with.endpoint.name", "literal name, not a reference"}},
		{"model empty", "endpoint: { name: cle1, model: '' }", []string{"with.endpoint.model", "model must be a non-empty string"}},
		{"model number", "endpoint: { name: cle1, model: 3 }", []string{"with.endpoint.model", "non-empty string"}},
		{"model list", "endpoint: { name: cle1, model: [a] }", []string{"with.endpoint.model", "non-empty string"}},
		{"effort unknown", "endpoint: { name: cle1, model: m, effort: huge }", []string{"with.endpoint.effort", "effort must be one of low, medium, high, xhigh, max"}},
		{"effort none", "endpoint: { name: cle1, model: m, effort: none }", []string{"with.endpoint.effort", "effort must be one of"}},
		{"effort reference", "endpoint: { name: cle1, model: m, effort: '${EFFORT}' }", []string{"with.endpoint.effort", "effort must be one of"}},
		{"effort number", "endpoint: { name: cle1, model: m, effort: 3 }", []string{"with.endpoint.effort", "effort must be one of"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadChatStep(t, "      prompt: hi\n      "+tc.with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// The block replaces the keys of the old method. Each one with the block is a
// contradiction. The error names the two keys and the path of the block.
func TestChatCompletionEndpointBlockExcludesOldKeys(t *testing.T) {
	t.Parallel()

	block := "endpoint: { name: cle1, model: m }\n      "
	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"model", "model: m", []string{"with.endpoint", `key "endpoint" and key "model" contradict each other; the block endpoint gives the model`}},
		{"model list", "model: [{ name: m, endpoint: cle2 }]", []string{"with.endpoint", `key "endpoint" and key "model" contradict each other`}},
		{"thinking", "thinking: { enabled: true, effort: high }", []string{"with.endpoint", `key "endpoint" and key "thinking" contradict each other; the block endpoint gives the effort`}},
		{"api_key_name", "api_key_name: KEY", []string{"with.endpoint", `key "endpoint" and key "api_key_name" contradict each other; an endpoint holds its own key`}},
		{"base_url", "base_url: https://example.com", []string{"with.endpoint", `key "endpoint" and key "base_url" contradict each other; an endpoint holds its own base URL`}},
		{"provider", "provider: openai", []string{"with.endpoint", `key "endpoint" and key "provider" contradict each other; an endpoint gives the provider type`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadChatStep(t, "      prompt: hi\n      "+block+tc.with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// Spec 094, "chat.completion": in an entry of the list, endpoint with api_key_name,
// base_url or provider is a contradiction. The error names the two keys and the
// path.
func TestChatCompletionEntryEndpointContradictions(t *testing.T) {
	t.Parallel()

	reasons := map[string]string{
		"api_key_name": "an endpoint holds its own key",
		"base_url":     "an endpoint holds its own base URL",
		"provider":     "an endpoint gives the provider type",
	}
	values := map[string]string{"api_key_name": "KEY", "base_url": "https://example.com", "provider": "openai"}
	for key, reason := range reasons {
		t.Run("entry with "+key, func(t *testing.T) {
			t.Parallel()
			_, err := loadChatStep(t, "      model:\n        - name: ok\n          endpoint: cle0\n        - name: m\n          endpoint: cle1\n          "+key+": "+values[key]+"\n      prompt: hi")
			require.Error(t, err)
			assert.Contains(t, err.Error(), `key "endpoint" and key "`+key+`" contradict each other; `+reason)
			assert.Contains(t, err.Error(), "with.model[1].endpoint", "the error names the entry")
		})
	}
}

// The endpoint of an entry is a literal name.
func TestChatCompletionEntryEndpointErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		with string
		want []string
	}{
		{"not a name", "endpoint: A_b", []string{"with.model[0].endpoint", "must match"}},
		{"reference", "endpoint: ${E}", []string{"with.model[0].endpoint", "not a reference"}},
		{"number", "endpoint: 3", []string{"with.model[0].endpoint", "must be a non-empty string"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadChatStep(t, "      prompt: hi\n      model:\n        - name: m\n          "+tc.with)
			require.Error(t, err)
			for _, part := range tc.want {
				assert.Contains(t, err.Error(), part)
			}
		})
	}
}

// An endpoint belongs to a chat.completion step. The llm block of the DAG, which
// an agent DAG uses for its decisions, and the llm block of another action refuse
// it.
func TestEndpointIsOnlyForChatCompletion(t *testing.T) {
	t.Parallel()

	for name, yamlText := range map[string]string{
		"dag llm": `
llm:
  endpoint: cle1
  model: m
steps:
  - id: s
    run: echo hi
`,
		"dag llm model entry": `
llm:
  model:
    - name: m
      endpoint: cle1
steps:
  - id: s
    run: echo hi
`,
		"agent dag": `
type: agent
llm:
  endpoint: cle1
  model: m
steps:
  - name: work
    run: echo work
tasks:
  - name: done
    description: Done.
`,
		"browser.run": `
steps:
  - id: s
    action: browser.run
    with:
      url: https://example.com
      do: [{ wait: 1s }]
      llm:
        endpoint: cle1
        model: m
`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(t.Context(), []byte(yamlText), spec.WithDAGsDir(t.TempDir()))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "endpoint")
		})
	}
}

// The step key llm of the older form has no block endpoint: the key endpoint is
// valid only in the with of a chat.completion step. Its model list takes the key
// endpoint in each entry, with the rules of the with.
func TestChatStepLLMBlockEndpoint(t *testing.T) {
	t.Parallel()

	load := func(llm string) (*ir.DAG, error) {
		return spec.LoadYAML(t.Context(), []byte("steps:\n  - id: s\n    type: chat\n    llm:\n"+llm+"\n    messages:\n      - role: user\n        content: hi\n"),
			spec.WithDAGsDir(t.TempDir()))
	}
	for _, llm := range []string{
		"      endpoint: cle1\n      model: m",
		"      endpoint: { name: cle1, model: m }",
	} {
		_, err := load(llm)
		require.Error(t, err, llm)
		assert.Contains(t, err.Error(), "the key endpoint is valid only with the action chat.completion", llm)
	}

	dag, err := load("      model:\n        - name: m\n          endpoint: cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"cle1"}, dag.Steps[0].AIEndpoints())

	_, err = load("      model:\n        - name: m\n          endpoint: cle1\n          provider: openai")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm.model[0].endpoint")
	assert.Contains(t, err.Error(), `key "endpoint" and key "provider" contradict each other`)
}

// A handler and a step of the map syntax decode the llm key before the loader
// reads the step, so they get the same error for the key endpoint.
func TestLLMEndpointOfAHandlerIsAnError(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(t.Context(), []byte(`
handler_on:
  failure:
    type: chat
    llm: { endpoint: cle1, model: m }
    messages: [{ role: user, content: hi }]
steps:
  - id: s
    run: echo hi
`), spec.WithDAGsDir(t.TempDir()))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the key endpoint is valid only with the action chat.completion")
}
