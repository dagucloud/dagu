// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const trustSecrets = `
secrets:
  - name: GIT_TOKEN
    provider: env
    key: SRC_GIT_TOKEN
  - name: API_KEY
    provider: env
    key: SRC_API_KEY
`

// secretsUntrustedDefaultError is the error of Spec 092 for a step that lists
// secrets and is untrusted by default.
const secretsUntrustedDefaultError = "step lists secrets but is untrusted by default in a DAG that has an agent.run step; " +
	"add untrusted: false to run it as the process user, or remove the secrets"

// Spec 092, "Errors": each case that dagu validate rejects for the keys
// secrets and untrusted.
func TestStepTrustValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		yaml  string
		wants []string
	}{
		{
			name: "undeclared secret",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: [NOPE]
    run: echo hi
`,
			wants: []string{`step "push"`, `"NOPE"`, "does not declare"},
		},
		{
			name: "duplicate secret",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: [GIT_TOKEN, GIT_TOKEN]
    run: echo hi
`,
			wants: []string{`step "push"`, `"GIT_TOKEN"`, "occurs two times"},
		},
		{
			name: "secrets on an untrusted step",
			yaml: trustSecrets + `
steps:
  - id: verify
    untrusted: true
    secrets: [GIT_TOKEN]
    run: ./test.sh
`,
			wants: []string{`untrusted step "verify"`, "cannot have the key secrets"},
		},
		{
			name: "secrets on an agent.run step with a CLI provider",
			yaml: trustSecrets + `
steps:
  - id: code
    action: agent.run
    secrets: [API_KEY]
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
`,
			wants: []string{`untrusted step "code"`, "cannot have the key secrets"},
		},
		{
			name: "reference to a secret that the step does not get",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: [GIT_TOKEN]
    run: echo ${GIT_TOKEN} ${API_KEY}
`,
			wants: []string{`step "push"`, `"API_KEY"`, "does not get"},
		},
		{
			name: "reference in an untrusted step",
			yaml: trustSecrets + `
steps:
  - id: verify
    untrusted: true
    run: echo $GIT_TOKEN
`,
			wants: []string{`step "verify"`, `"GIT_TOKEN"`, "does not get"},
		},
		{
			name: "reference in a step with no list",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: [GIT_TOKEN]
    run: echo ok
  - id: other
    env:
      - TOKEN: ${env.GIT_TOKEN}
    run: echo $TOKEN
`,
			wants: []string{`step "other"`, `"GIT_TOKEN"`},
		},
		{
			name: "output of an untrusted step in the command of a trusted step",
			yaml: `
steps:
  - id: verify
    untrusted: true
    run: echo "verdict=ok" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: verdict
  - id: gate
    depends: [verify]
    run: echo '${steps.verify.outputs.verdict}'
`,
			wants: []string{`step "gate"`, "${steps.verify.outputs.verdict}", `an output of the untrusted step "verify"`, "pass it through env"},
		},
		{
			name: "output of an untrusted step in the shell of a trusted step",
			yaml: `
steps:
  - id: verify
    untrusted: true
    run: echo "shell=sh" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: shell
  - id: gate
    depends: [verify]
    shell: ${steps.verify.outputs.shell}
    command: echo hi
`,
			wants: []string{`the field steps[1].shell refers to ${steps.verify.outputs.shell}`},
		},
		{
			name: "output of an untrusted step in a shell argument of a trusted step",
			yaml: `
steps:
  - id: verify
    untrusted: true
    run: echo "flag=-e" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: flag
  - id: gate
    depends: [verify]
    shell: sh
    shell_args: ["${steps.verify.outputs.flag}"]
    command: echo hi
`,
			wants: []string{`the field steps[1].shell_args[0] refers to ${steps.verify.outputs.flag}`},
		},
		{
			name: "output of an agent.run step with a CLI provider in a script",
			yaml: `
steps:
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: gate
    depends: [code]
    untrusted: false
    script: |
      echo ${code.outputs.tool_calls}
`,
			wants: []string{`step "gate"`, `an output of the untrusted step "code"`},
		},
		{
			name: "output of a step that is untrusted by default, in the command of a trusted step",
			yaml: `
steps:
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: verify
    depends: [code]
    run: echo "verdict=ok" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: verdict
  - id: gate
    depends: [verify]
    untrusted: false
    run: echo '${steps.verify.outputs.verdict}'
`,
			wants: []string{`step "gate"`, "${steps.verify.outputs.verdict}", `an output of the untrusted step "verify"`},
		},
		{
			name: "untrusted true with secrets in a DAG with an agent.run step",
			yaml: trustSecrets + `
steps:
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: push
    depends: [code]
    untrusted: true
    secrets: [GIT_TOKEN]
    run: echo hi
`,
			wants: []string{`untrusted step "push"`, "cannot have the key secrets"},
		},
		{
			name: "secrets on a step that is untrusted by default",
			yaml: trustSecrets + `
steps:
` + trustAgentStep + `
  - id: push
    depends: [code]
    secrets: [GIT_TOKEN]
    run: echo hi
`,
			wants: []string{"field 'steps[1].secrets'", secretsUntrustedDefaultError},
		},
		{
			name: "empty secrets on a step that is untrusted by default",
			yaml: trustSecrets + `
steps:
` + trustAgentStep + `
  - id: push
    depends: [code]
    secrets: []
    run: echo hi
`,
			wants: []string{"field 'steps[1].secrets'", secretsUntrustedDefaultError},
		},
		{
			name: "secrets on a script step that is untrusted by default",
			yaml: trustSecrets + `
steps:
` + trustAgentStep + `
  - id: push
    depends: [code]
    secrets: [GIT_TOKEN]
    script: |
      echo hi
`,
			wants: []string{"field 'steps[1].secrets'", secretsUntrustedDefaultError},
		},
		{
			name: "secrets on a harness.run step that is untrusted by default",
			yaml: trustSecrets + `
steps:
` + trustAgentStep + `
  - id: push
    depends: [code]
    secrets: [GIT_TOKEN]
    action: harness.run
    with:
      provider: claude
      prompt: hello
`,
			wants: []string{"field 'steps[1].secrets'", secretsUntrustedDefaultError},
		},
		{
			name: "secrets on a handler step that is untrusted by default",
			yaml: trustSecrets + `
steps:
` + trustAgentStep + `
handler_on:
  exit:
    secrets: [GIT_TOKEN]
    run: echo hi
`,
			wants: []string{"field 'handler_on.exit.secrets'", secretsUntrustedDefaultError},
		},
		{
			name: "untrusted false on an action in a DAG with an agent.run step",
			yaml: `
steps:
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: log
    depends: [code]
    untrusted: false
    action: log.write
    with:
      message: hi
`,
			wants: []string{"untrusted is valid only on a step with run, command or script"},
		},
		{
			name: "secret reference in a step that is untrusted by default",
			yaml: trustSecrets + `
steps:
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: verify
    depends: [code]
    run: echo ${GIT_TOKEN}
`,
			wants: []string{`step "verify"`, `"GIT_TOKEN"`, "does not get"},
		},
		{
			name: "untrusted on an action",
			yaml: `
steps:
  - id: log
    untrusted: true
    action: log.write
    with:
      message: hi
`,
			wants: []string{"untrusted is valid only on a step with run, command or script"},
		},
		{
			name: "untrusted false on an action",
			yaml: `
steps:
  - id: log
    untrusted: false
    action: log.write
    with:
      message: hi
`,
			wants: []string{"untrusted is valid only on a step with run, command or script"},
		},
		{
			name: "untrusted with container",
			yaml: `
steps:
  - id: verify
    untrusted: true
    container: { image: alpine }
    run: ./test.sh
`,
			wants: []string{"untrusted is not valid on a step with container"},
		},
		{
			name: "untrusted in a DAG with a container",
			yaml: `
container: { image: alpine }
steps:
  - id: verify
    untrusted: true
    run: ./test.sh
`,
			wants: []string{"container of the DAG"},
		},
		{
			name: "secret name with spaces",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: [" GIT_TOKEN"]
    run: echo hi
`,
			wants: []string{`the list secrets names " GIT_TOKEN", which the secrets block of the DAG does not declare`},
		},
		{
			name: "untrusted in defaults",
			yaml: `
defaults:
  untrusted: true
steps:
  - run: echo hi
`,
			wants: []string{"defaults.untrusted", `the key "untrusted" is not valid in defaults; set it on each step`},
		},
		{
			name: "untrusted false on an agent.run step with a CLI provider",
			yaml: `
steps:
  - id: code
    untrusted: false
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
`,
			wants: []string{"always untrusted", "remove untrusted: false"},
		},
		{
			name: "untrusted on a foreach body step",
			yaml: `
steps:
  - id: each
    foreach:
      items: [a, b]
      steps:
        - id: body
          untrusted: true
          run: echo ${foreach.item}
`,
			wants: []string{"foreach.steps[0].untrusted", `the key "untrusted" is not valid in a step of foreach.steps`},
		},
		{
			name: "secrets is a list",
			yaml: trustSecrets + `
steps:
  - id: push
    secrets: GIT_TOKEN
    run: echo hi
`,
			wants: []string{"secrets"},
		},
		{
			name: "untrusted is a boolean",
			yaml: `
steps:
  - id: verify
    untrusted: "yes"
    run: ./test.sh
`,
			wants: []string{"untrusted"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := spec.LoadYAML(t.Context(), []byte(tc.yaml))
			require.Error(t, err)
			for _, want := range tc.wants {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// The loader keeps the list, marks the untrusted steps, and computes the step
// scope once.
func TestStepTrustBuild(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
  - id: clone
    untrusted: false
    secrets: [GIT_TOKEN]
    run: echo "$GIT_TOKEN" > /dev/null
  - id: verify
    depends: [clone]
    untrusted: true
    run: ./test.sh
  - id: code
    depends: [verify]
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
  - id: plain
    depends: [code]
    run: echo plain
handler_on:
  exit:
    untrusted: false
    secrets: [API_KEY]
    run: echo "${API_KEY}" > /dev/null
`))
	require.NoError(t, err)
	assert.True(t, dag.SecretStepScope)
	require.Len(t, dag.Steps, 4)
	assert.Equal(t, []string{"GIT_TOKEN"}, dag.Steps[0].Secrets)
	assert.False(t, dag.Steps[0].Untrusted)
	assert.True(t, dag.Steps[1].Untrusted)
	assert.True(t, dag.Steps[2].Untrusted, "an agent.run step with a CLI provider is untrusted")
	assert.Nil(t, dag.Steps[3].Secrets)
	require.NotNil(t, dag.HandlerOn.Exit)
	assert.Equal(t, []string{"API_KEY"}, dag.HandlerOn.Exit.Secrets)

	assert.False(t, dag.SecretScopeOf(dag.Steps[3]).Allows("GIT_TOKEN"), "a step with no list gets no secret")
	assert.True(t, dag.SecretScopeOf(dag.Steps[0]).Allows("GIT_TOKEN"))
	assert.False(t, dag.SecretScopeOf(dag.Steps[0]).Allows("API_KEY"))
	assert.False(t, dag.SecretScopeOf(dag.Steps[1]).Allows("GIT_TOKEN"))
}

// A DAG with no untrusted step and no list keeps Spec 069: each step gets each
// secret, and a reference to a secret is valid in each step.
func TestStepTrustNoStepScope(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
  - id: a
    run: echo ${GIT_TOKEN} ${API_KEY} > /dev/null
`))
	require.NoError(t, err)
	assert.False(t, dag.SecretStepScope)
	assert.True(t, dag.SecretScopeOf(dag.Steps[0]).Allows("GIT_TOKEN"))
}

// An output of an untrusted step is valid in the env, the preconditions, the
// repeat_policy and the with of an action of a trusted step, and in the command
// of another untrusted step.
func TestUntrustedOutputOutsideCommands(t *testing.T) {
	t.Parallel()

	_, err := spec.LoadYAML(t.Context(), []byte(`
steps:
  - id: verify
    untrusted: true
    run: echo "verdict=ok" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: verdict
  - id: gate
    depends: [verify]
    env:
      - VERDICT: ${steps.verify.outputs.verdict}
    preconditions:
      - condition: ${steps.verify.outputs.verdict}
        expected: ok
    run: printf '%s\n' "$VERDICT"
  - id: note
    depends: [verify]
    action: log.write
    with:
      message: ${steps.verify.outputs.verdict}
  - id: again
    depends: [verify]
    untrusted: true
    run: echo '${steps.verify.outputs.verdict}'
`))
	require.NoError(t, err)
}

// A step in the body of a foreach step is untrusted by default in a DAG that has
// an agent.run step, so its command can hold an output of an untrusted step. In
// another DAG, the body step is trusted, and the same command is an error.
func TestUntrustedOutputInAForeachBody(t *testing.T) {
	t.Parallel()

	body := `
  - id: verify
    depends: [code]
    run: echo "verdict=ok" > "$DAGU_OUTPUT_FILE"
    outputs:
      - name: verdict
  - id: each
    depends: [verify]
    foreach:
      items: [a, b]
      steps:
        - id: body
          run: echo '${steps.verify.outputs.verdict}'
`
	_, err := spec.LoadYAML(t.Context(), []byte("steps:"+trustAgentStep+body))
	require.NoError(t, err)

	_, err = spec.LoadYAML(t.Context(), []byte(`
steps:
  - id: code
    run: echo code
`+strings.Replace(body, "    depends: [code]\n", "    depends: [code]\n    untrusted: true\n", 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `an output of the untrusted step "verify"`)
}

// An empty list makes the step scope: the step gets no secret.
func TestStepTrustEmptyList(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
  - id: a
    secrets: []
    run: echo hi
`))
	require.NoError(t, err)
	assert.True(t, dag.SecretStepScope)
	assert.False(t, dag.SecretScopeOf(dag.Steps[0]).Allows("GIT_TOKEN"))
}

const trustAgentStep = `
  - id: code
    action: agent.run
    with:
      prompt: hello
      provider: claude
      endpoint:
        name: test-endpoint
        model: claude-test
`

// Spec 092, "Default of the key": in a DAG that has an agent.run step, a step
// that runs a process is untrusted unless it has untrusted: false. Secrets do
// not change the default. A step with another action keeps the value false.
func TestStepTrustAgentDefault(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
`+trustAgentStep+`
  - id: shell
    depends: [code]
    run: echo plain
  - id: command
    depends: [code]
    command: echo plain
  - id: script
    depends: [code]
    script: |
      echo plain
  - id: harness
    depends: [code]
    action: harness.run
    with:
      provider: claude
      prompt: hello
  - id: explicit_true
    depends: [code]
    untrusted: true
    run: echo plain
  - id: explicit_false
    depends: [code]
    untrusted: false
    run: echo plain
  - id: listed_false
    depends: [code]
    untrusted: false
    secrets: [GIT_TOKEN]
    run: echo plain
  - id: action
    depends: [code]
    action: log.write
    with:
      message: hi
  - id: each
    depends: [code]
    foreach:
      items: [a, b]
      steps:
        - id: body
          run: echo ${foreach.item}
handler_on:
  exit:
    run: echo exit
`))
	require.NoError(t, err)

	untrusted := map[string]bool{}
	for _, step := range dag.Steps {
		untrusted[step.Name] = step.Untrusted
		assert.Nil(t, step.UntrustedKey, "step %s: the loader clears the key", step.Name)
	}
	assert.Equal(t, map[string]bool{
		"code":           true,
		"shell":          true,
		"command":        true,
		"script":         true,
		"harness":        true,
		"explicit_true":  true,
		"explicit_false": false,
		"listed_false":   false,
		"action":         false,
		"each":           false,
	}, untrusted)

	var each *ir.Step
	for i := range dag.Steps {
		if dag.Steps[i].Name == "each" {
			each = &dag.Steps[i]
		}
	}
	require.NotNil(t, each)
	require.Len(t, each.Foreach.Steps, 1)
	assert.True(t, each.Foreach.Steps[0].Untrusted, "a body step cannot opt out")
	require.NotNil(t, dag.HandlerOn.Exit)
	assert.True(t, dag.HandlerOn.Exit.Untrusted, "a handler step is a step of the DAG")

	// The effective value is the stored value.
	data, err := json.Marshal(dag)
	require.NoError(t, err)
	var stored ir.DAG
	require.NoError(t, json.Unmarshal(data, &stored))
	for i := range stored.Steps {
		assert.Equal(t, dag.Steps[i].Untrusted, stored.Steps[i].IsUntrusted(), "step %s", stored.Steps[i].Name)
	}
	assert.False(t, dag.SecretScopeOf(dag.Steps[1]).Allows("GIT_TOKEN"), "an untrusted step gets no secret")
	assert.True(t, dag.SecretScopeOf(dag.Steps[7]).Allows("GIT_TOKEN"))
}

// In a DAG that has an agent.run step, a step that lists secrets and has
// untrusted: false runs as the process user and keeps its secrets. A step with
// an action that runs no local process lists secrets with no key.
func TestStepTrustAgentSecretsOptOut(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
`+trustAgentStep+`
  - id: push
    depends: [code]
    untrusted: false
    secrets: [GIT_TOKEN]
    run: echo "$GIT_TOKEN" > /dev/null
  - id: nothing
    depends: [code]
    untrusted: false
    secrets: []
    run: echo hi
  - id: checkout
    depends: [code]
    secrets: [API_KEY]
    action: log.write
    with:
      message: hi
  - id: child
    depends: [code]
    secrets: [API_KEY]
    call: child-dag
  - id: chat
    depends: [code]
    secrets: [API_KEY]
    action: chat.completion
    with:
      provider: local
      model: m
      prompt: hi
handler_on:
  exit:
    untrusted: false
    secrets: [GIT_TOKEN]
    run: echo exit
`))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 6)
	push := dag.Steps[1]
	assert.False(t, push.Untrusted)
	assert.Equal(t, []string{"GIT_TOKEN"}, push.Secrets)
	assert.True(t, dag.SecretScopeOf(push).Allows("GIT_TOKEN"))
	assert.False(t, dag.SecretScopeOf(push).Allows("API_KEY"))
	assert.False(t, dag.Steps[2].Untrusted)
	for _, i := range []int{3, 4, 5} {
		assert.False(t, dag.Steps[i].Untrusted, "step %s runs no local process", dag.Steps[i].Name)
		assert.Equal(t, []string{"API_KEY"}, dag.Steps[i].Secrets)
	}
	require.NotNil(t, dag.HandlerOn.Exit)
	assert.False(t, dag.HandlerOn.Exit.Untrusted)
	assert.Equal(t, []string{"GIT_TOKEN"}, dag.HandlerOn.Exit.Secrets)
}

// In a DAG with no agent.run step, secrets with no key is valid: the key
// decides alone, and the step gets its secrets.
func TestStepTrustNoAgentSecrets(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(trustSecrets+`
steps:
  - id: push
    secrets: [GIT_TOKEN]
    run: echo "$GIT_TOKEN" > /dev/null
  - id: empty
    secrets: []
    run: echo hi
`))
	require.NoError(t, err)
	assert.False(t, dag.Steps[0].Untrusted)
	assert.Equal(t, []string{"GIT_TOKEN"}, dag.Steps[0].Secrets)
	assert.False(t, dag.Steps[1].Untrusted)
}

// A DAG with no agent.run step keeps the old default: a step is untrusted only
// with untrusted: true. A sub-DAG step does not make its parent an agent DAG.
func TestStepTrustNoAgentDefault(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAML(t.Context(), []byte(`
steps:
  - id: plain
    run: echo plain
  - id: marked
    depends: [plain]
    untrusted: true
    run: echo marked
  - id: opened
    depends: [plain]
    untrusted: false
    run: echo opened
  - id: child
    depends: [plain]
    call: child-dag
`))
	require.NoError(t, err)
	assert.False(t, dag.Steps[0].Untrusted)
	assert.True(t, dag.Steps[1].Untrusted)
	assert.False(t, dag.Steps[2].Untrusted)
	assert.False(t, dag.Steps[3].Untrusted)
	assert.True(t, dag.SecretStepScope, "the marked step makes the step scope")
}
