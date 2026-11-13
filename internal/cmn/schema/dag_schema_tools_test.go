// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package schema

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const toolsDAGHead = `
steps:
  - id: ask
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
`

func TestDAGSchemaToolMap(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, toolsDAGHead+`
      max_tools: 8
      tool_result_max_bytes: 0
      dags:
        - name: lookup
        - name: refund
          params:
            CUSTOMER_ID: ${CUSTOMER}
            AMOUNT:
              from_ai: true
      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com/mcp
            headers:
              Authorization: Bearer x
            allow: [search]
`)
	require.NoError(t, resolved.Validate(doc))
}

// The list form of main, a list of DAG names, stays valid. It is deprecated.
func TestDAGSchemaToolListForm(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, toolsDAGHead+`
      tools:
        - lookup
        - refund
`)
	require.NoError(t, resolved.Validate(doc))

	llmDoc := mustParseYAMLDocument(t, `
llm:
  provider: local
  model: m
  tools: [lookup]
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`)
	require.NoError(t, resolved.Validate(llmDoc))
}

func TestDAGSchemaRejectsBadToolMaps(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for name, tools := range map[string]string{
		"list of objects":         "- name: lookup",
		"list with an empty name": "- ''",
		"string in place of map":  "lookup",
		"unknown category":        "toolsets:\n          - name: support",
		"builtin on chat":         "builtin:\n          read: true",
		"dags in the tool map":    "dags:\n          - name: lookup",
		"actions category":        "actions:\n          - name: a",
		"if on an mcp entry":      "mcp:\n          - name: docs\n            url: http://x\n            allow: [a]\n            if: ['true']",
		"params on mcp":           "mcp:\n          - name: docs\n            url: http://x\n            allow: [a]\n            params: {A: b}",
		"mcp no allow":            "mcp:\n          - name: docs\n            url: http://x",
		"mcp no name":             "mcp:\n          - url: http://x\n            allow: [a]",
		"mcp bad name":            "mcp:\n          - name: Docs\n            url: http://x\n            allow: [a]",
		"mcp timeout":             "mcp:\n          - name: docs\n            url: http://x\n            allow: [a]\n            timeout: 5s",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, toolsDAGHead+"      tools:\n        "+tools+"\n")
			require.Error(t, resolved.Validate(doc))
		})
	}
}

// The key dags of the with holds the DAG tools. Its entries have the keys name
// and params only.
func TestDAGSchemaRejectsBadDAGEntries(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for name, dags := range map[string]string{
		"map in place of list":    "{ name: lookup }",
		"plain string entry":      "- lookup",
		"no name":                 "- params: {A: b}",
		"unknown key":             "- name: a\n          bogus: 1",
		"if on a dag entry":       "- name: a\n          if: ['true']",
		"description on an entry": "- name: a\n          description: d",
		"name too long":           "- name: " + strings.Repeat("a", 65),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, toolsDAGHead+"      dags:\n        "+dags+"\n")
			require.Error(t, resolved.Validate(doc))
		})
	}

	// The llm block takes the key dags in the same way.
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, `
llm:
  provider: local
  model: m
  dags: [{ name: lookup }]
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`)))
	require.Error(t, resolved.Validate(mustParseYAMLDocument(t, `
llm:
  provider: local
  model: m
  tools: { dags: [{ name: lookup }] }
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`)))
}

// The skills key lists the names of skill folders, with the name rule of Spec 096:
// 1 to 64 lower case letters, digits and single hyphens, with no hyphen at an
// edge. An object item is not valid.
func TestDAGSchemaSkills(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, toolsDAGHead+`
      skills:
        - refund-policy
        - tone-of-voice
        - 2fa-steps
        - `+strings.Repeat("a", 64)+`
`)
	require.NoError(t, resolved.Validate(doc))

	llmDoc := mustParseYAMLDocument(t, `
llm:
  provider: local
  model: m
  skills: [refund-policy]
steps:
  - id: ask
    action: chat.completion
    with:
      prompt: hi
`)
	require.NoError(t, resolved.Validate(llmDoc))

	for name, skills := range map[string]string{
		"bad name":      "- Refund_Policy",
		"double hyphen": "- a--b",
		"edge hyphen":   "- a-",
		"too long":      "- " + strings.Repeat("a", 65),
		"object item":   "- skill: a",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			doc := mustParseYAMLDocument(t, toolsDAGHead+"      skills:\n        "+skills+"\n")
			require.Error(t, resolved.Validate(doc))
		})
	}
}

const agentRunDAGHead = `
steps:
  - id: review
    action: agent.run
    output_schema:
      type: object
      additionalProperties: false
      properties:
        verdict: { type: string }
    with:
      prompt: Review the patch.
`

// The action agent.run has its own with. The limits and the three categories of
// the tool map and the skills are valid.
func TestDAGSchemaAgentRun(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	doc := mustParseYAMLDocument(t, agentRunDAGHead+`
      provider: claude
      endpoint:
        name: cle1
        model: sonnet
      system: Report only real defects.
      max_tool_iterations: 20
      max_tool_calls: 50
      dags:
        - name: lookup-ticket
          params:
            CUSTOMER_ID: c-42
            QUESTION: { from_ai: { description: The question } }
      tools:
        mcp:
          - name: docs
            url: https://mcp.example.com/mcp
            headers: { Authorization: "Bearer ${DOCS_TOKEN}" }
            allow: [search]
        builtin:
          read: true
          search: true
          shell:
            allow: ['git diff *']
            deny: ['git push *']
      skills:
        - tone
`)
	require.NoError(t, resolved.Validate(doc))
}

// The key effort of agent.run is one of the five levels, or a reference.
func TestDAGSchemaAgentRunEffort(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max", "${EFFORT}"} {
		doc := mustParseYAMLDocument(t, agentRunDAGHead+"      provider: claude\n      endpoint:\n        name: cle1\n        model: sonnet\n        effort: \""+effort+"\"\n")
		require.NoError(t, resolved.Validate(doc), effort)
	}
	for _, effort := range []string{"extreme", "High", "3", "none"} {
		doc := mustParseYAMLDocument(t, agentRunDAGHead+"      provider: claude\n      endpoint:\n        name: cle1\n        model: sonnet\n        effort: \""+effort+"\"\n")
		require.Error(t, resolved.Validate(doc), effort)
	}
}

// The with of agent.run is closed: a key that it does not have is invalid, as is
// a value out of the range. Each case adds one key to a valid step of the
// provider claude, unless it names its own provider.
func TestDAGSchemaAgentRunRejects(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	const claude = "provider: claude\n      endpoint: { name: cle1, model: m }\n      "
	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, agentRunDAGHead+"      "+claude+"\n")))
	for name, with := range map[string]string{
		"unknown key":         "bogus: 1",
		"messages":            "messages: []",
		"engine":              "engine: cli",
		"fallback":            "fallback: [{ provider: codex }]",
		"cli block":           "cli: { permission_mode: plan }",
		"rate_limit_wait_sec": "rate_limit_wait_sec: 3600",
		"on_rate_limited":     "on_rate_limited: wait",
		"max_wait_sec":        "max_wait_sec: 60",
		"rate_limit_poll_sec": "rate_limit_poll_sec: 60",
		"on_auth_required":    "on_auth_required: fail",
		"max_tools":           "max_tools: 4",
		"tool_result":         "tool_result_max_bytes: 4",
		"api block":           "api: { temperature: 0.2 }",
		"top-level api key":   "temperature: 0.2",
		"base_url":            "base_url: http://localhost:1",
		"provider reference":  "provider: ${PROVIDER}",
		"max turns":           "max_turns: 3",
		"agent file":          "agent: reviewer",
		"iterations zero":     "max_tool_iterations: 0",
		"tool entry if":       "dags: [{ name: a, if: ['true'] }]",
		"dags in tools":       "tools: { dags: [{ name: a }] }",
		"dags not a list":     "dags: { name: a }",
		"actions category":    "tools: { actions: [{ name: a }] }",
		"mcp entry if":        "tools: { mcp: [{ name: docs, url: 'http://x', allow: [a], if: ['true'] }] }",
		"mcp named dagu":      "tools: { mcp: [{ name: dagu, url: 'http://x', allow: [a] }] }",
		"max tool calls zero": "max_tool_calls: 0",
		"unknown category":    "tools: { toolsets: [{ name: a }] }",
		"shell true":          "tools: { builtin: { shell: true } }",
		"shell star":          "tools: { builtin: { shell: { allow: ['*'] } } }",
		"builtin key":         "tools: { builtin: { delete: true } }",
		"tools list":          "tools: [lookup]",
		"endpoint not name":   "provider: claude\n      endpoint: { name: Not_A_Name, model: m }",
		"endpoint empty":      "provider: claude\n      endpoint: { name: '', model: m }",
		"endpoint reference":  "provider: claude\n      endpoint: { name: '${ENDPOINT}', model: m }",
		"endpoint number":     "provider: claude\n      endpoint: { name: 3, model: m }",
		"endpoint too long":   "provider: claude\n      endpoint: { name: a" + strings.Repeat("b", 64) + ", model: m }",
		"endpoint string":     "provider: claude\n      endpoint: cle1",
		"endpoint no name":    "provider: claude\n      endpoint: { model: m }",
		"endpoint no model":   "provider: claude\n      endpoint: { name: cle1 }",
		"endpoint unknown":    "provider: claude\n      endpoint: { name: cle1, model: m, temperature: 1 }",
		"flat model":          "provider: claude\n      endpoint: { name: cle1, model: m }\n      model: m",
		"flat effort":         "provider: claude\n      endpoint: { name: cle1, model: m }\n      effort: high",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !strings.HasPrefix(with, "provider:") {
				with = claude + with
			}
			doc := mustParseYAMLDocument(t, agentRunDAGHead+"      "+with+"\n")
			require.Error(t, resolved.Validate(doc))
		})
	}
}

// The loader requires the prompt and the provider. A CLI provider other than
// claude supports only the model and the prompt in the with.
func TestDAGSchemaAgentRunRequiredAndOtherProviders(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	noPrompt := mustParseYAMLDocument(t, "steps:\n  - action: agent.run\n    with: { provider: codex }\n")
	require.Error(t, resolved.Validate(noPrompt))
	noProvider := mustParseYAMLDocument(t, "steps:\n  - action: agent.run\n    with: { prompt: hi }\n")
	require.Error(t, resolved.Validate(noProvider))

	require.NoError(t, resolved.Validate(mustParseYAMLDocument(t, agentRunDAGHead+"      provider: codex\n      model: gpt-x\n")))
	for _, key := range []string{
		"endpoint: { name: cle1, model: m }",
		"system: be brief",
		"dags: [{ name: lookup }]",
		"tools: { mcp: [{ name: docs, url: 'http://x', allow: [a] }] }",
		"skills: [tone]",
		"max_tool_iterations: 3",
		"max_tool_calls: 3",
	} {
		doc := mustParseYAMLDocument(t, agentRunDAGHead+"      provider: codex\n      "+key+"\n")
		require.Error(t, resolved.Validate(doc), key)
	}
}

// The key endpoint of agent.run is the name of an AI endpoint (Spec 094).
func TestDAGSchemaAgentRunEndpoint(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for _, with := range []string{
		"provider: claude\n      endpoint:\n        name: cle1\n        model: sonnet",
		"provider: claude\n      endpoint:\n        name: my-endpoint-2\n        model: claude-sonnet-4-6",
		"provider: claude\n      endpoint:\n        name: " + "a" + strings.Repeat("b", 63) + "\n        model: sonnet",
		"provider: claude\n      endpoint:\n        name: cle1\n        model: ${MODEL}\n        effort: ${EFFORT}",
	} {
		doc := mustParseYAMLDocument(t, agentRunDAGHead+"      "+with+"\n")
		require.NoError(t, resolved.Validate(doc), with)
	}
}

// The provider claude needs an endpoint block with a name and a model. A provider
// that no endpoint serves needs neither. The effort stays optional.
func TestDAGSchemaAgentRunEndpointRequired(t *testing.T) {
	t.Parallel()

	resolved := mustResolveDAGSchema(t)
	for _, with := range []string{
		"provider: claude",
		"provider: claude\n      model: sonnet",
		"provider: claude\n      endpoint:\n        name: cle1",
		"provider: claude\n      endpoint:\n        name: cle1\n        effort: high",
	} {
		doc := mustParseYAMLDocument(t, agentRunDAGHead+"      "+with+"\n")
		require.Error(t, resolved.Validate(doc), with)
	}
	doc := mustParseYAMLDocument(t, agentRunDAGHead+"      provider: codex\n")
	require.NoError(t, resolved.Validate(doc))
}
