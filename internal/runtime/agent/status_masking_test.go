// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
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
