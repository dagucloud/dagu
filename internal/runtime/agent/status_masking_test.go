// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Artifact paths carry runtime-resolved text just like the prompt, so a secret
// reaches persisted status through either field.
func TestMaskNodeSecretsMasksHumanTask(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"DEPLOY_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	node := &ir.Node{
		Step: ir.Step{HumanTask: &ir.HumanTaskConfig{
			Prompt:    "Review very-secret-token",
			Artifacts: []string{"reports/very-secret-token.html", "changes.diff"},
		}},
	}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.Step.HumanTask)
	assert.NotContains(t, node.Step.HumanTask.Prompt, "very-secret-token")
	require.Len(t, node.Step.HumanTask.Artifacts, 2)
	assert.NotContains(t, node.Step.HumanTask.Artifacts[0], "very-secret-token")
	assert.Equal(t, "changes.diff", node.Step.HumanTask.Artifacts[1])
}

func TestMaskNodeSecretsMasksStatusDetailLabels(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"CUSTOMER_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	node := &ir.Node{StatusDetails: []ir.NodeStatusDetail{
		{Label: "customer (TOKEN=very-secret-token)", Status: ir.NodeFailed},
	}}

	maskNodeSecrets(masker, node)

	require.Len(t, node.StatusDetails, 1)
	assert.Contains(t, node.StatusDetails[0].Label, "customer")
	assert.NotContains(t, node.StatusDetails[0].Label, "very-secret-token")
	assert.Equal(t, ir.NodeFailed, node.StatusDetails[0].Status)
}

func TestMaskNodeSecretsMasksAgentSession(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"PORTAL_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	session := &ir.AgentSession{
		LastError: "login rejected very-secret-token",
		Events:    []ir.AgentSessionEvent{{Content: "typed very-secret-token"}},
	}
	node := &ir.Node{AgentSession: session}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.AgentSession)
	assert.NotContains(t, node.AgentSession.LastError, "very-secret-token")
	assert.NotContains(t, node.AgentSession.Events[0].Content, "very-secret-token")
	assert.Contains(t, session.Events[0].Content, "very-secret-token", "the live session is not modified")
}

// Outputs a step publishes, such as those written to DAGU_OUTPUT_FILE, are
// stored as JSON text, so a secret reaches persisted status through them too.
func TestMaskNodeSecretsMasksStepOutputs(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"API_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	live := `{"header":"Bearer very-secret-token","region":"us"}`
	node := &ir.Node{StepOutputsValue: &live}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.StepOutputsValue)
	assert.JSONEq(t, `{"header":"Bearer *******","region":"us"}`, *node.StepOutputsValue)
	assert.Equal(t, `{"header":"Bearer very-secret-token","region":"us"}`, live, "the live outputs are not modified")
}

// Human-task outputs are operator input, and a resumed run reads them back
// from status, so they are stored as entered.
func TestMaskNodeSecretsKeepsHumanTaskOutputs(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"API_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	entered := `{"note":"very-secret-token"}`
	node := &ir.Node{
		Step:             ir.Step{HumanTask: &ir.HumanTaskConfig{Prompt: "Confirm"}},
		StepOutputsValue: &entered,
	}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.StepOutputsValue)
	assert.Equal(t, `{"note":"very-secret-token"}`, *node.StepOutputsValue)
}

// A secret can match a JSON number, boolean, or null, where plain replacement
// would leave the stored outputs unreadable for a run that reuses them.
func TestMaskNodeSecretsKeepsOutputsValidJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		secret   string
		outputs  string
		expected string
	}{
		{
			name:     "Number",
			secret:   "4242",
			outputs:  `{"count":4242,"note":"pin 4242","region":"us"}`,
			expected: `{"count":"*******","note":"pin *******","region":"us"}`,
		},
		{
			name:     "Boolean",
			secret:   "true",
			outputs:  `{"enabled":true,"note":"true story"}`,
			expected: `{"enabled":"*******","note":"******* story"}`,
		},
		{
			name:     "Null",
			secret:   "null",
			outputs:  `{"note":"null value","parent":null}`,
			expected: `{"note":"******* value","parent":"*******"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			masker := newStatusSecretMasker([]string{"SECRET=" + tt.secret})
			require.NotNil(t, masker)
			outputs := tt.outputs
			node := &ir.Node{OutputsValue: &outputs, StepOutputsValue: &outputs}

			maskNodeSecrets(masker, node)

			require.NotNil(t, node.OutputsValue)
			assert.JSONEq(t, tt.expected, *node.OutputsValue)
			require.NotNil(t, node.StepOutputsValue)
			assert.JSONEq(t, tt.expected, *node.StepOutputsValue)
		})
	}
}

// A secret can span JSON values, so no single decoded value holds it. Keeping
// the document valid must not store the secret again.
func TestMaskNodeSecretsMasksSecretAcrossOutputValues(t *testing.T) {
	t.Parallel()

	const secret = `x","b`
	masker := newStatusSecretMasker([]string{"SECRET=" + secret})
	require.NotNil(t, masker)
	outputs := `{"a":"x","b":"y"}`
	node := &ir.Node{OutputsValue: &outputs, StepOutputsValue: &outputs}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.OutputsValue)
	assert.NotContains(t, *node.OutputsValue, secret)
	require.NotNil(t, node.StepOutputsValue)
	assert.NotContains(t, *node.StepOutputsValue, secret)
}

// Stored outputs are JSON text, written both with and without HTML escaping,
// so a secret with a quote, a backslash, a control character, or <, >, and &
// appears there only in escaped form.
func TestMaskNodeSecretsMasksEscapedSecret(t *testing.T) {
	t.Parallel()

	const secret = "q\"b\\s\n\x01<a>& z"
	masker := newStatusSecretMasker([]string{"API_TOKEN=" + secret})
	require.NotNil(t, masker)

	for name, escapeHTML := range map[string]bool{"HTMLEscaped": true, "Unescaped": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			encoder := json.NewEncoder(&buf)
			encoder.SetEscapeHTML(escapeHTML)
			require.NoError(t, encoder.Encode(map[string]string{"token": "Bearer " + secret}))
			stored := strings.TrimSuffix(buf.String(), "\n")
			require.NotContains(t, stored, secret)
			node := &ir.Node{OutputsValue: &stored, StepOutputsValue: &stored}

			maskNodeSecrets(masker, node)

			require.NotNil(t, node.OutputsValue)
			assert.JSONEq(t, `{"token":"Bearer *******"}`, *node.OutputsValue)
			require.NotNil(t, node.StepOutputsValue)
			assert.JSONEq(t, `{"token":"Bearer *******"}`, *node.StepOutputsValue)
		})
	}
}
