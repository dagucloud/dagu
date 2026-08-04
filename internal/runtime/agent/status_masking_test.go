// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMaskNodeSecretsMasksHumanTaskPrompt(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"DEPLOY_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	node := &exec.Node{
		Step: core.Step{HumanTask: &core.HumanTaskConfig{
			Prompt: "Review very-secret-token",
		}},
	}

	maskNodeSecrets(masker, node)

	require.NotNil(t, node.Step.HumanTask)
	assert.NotContains(t, node.Step.HumanTask.Prompt, "very-secret-token")
}

func TestMaskNodeSecretsMasksStatusDetailLabels(t *testing.T) {
	t.Parallel()

	masker := newStatusSecretMasker([]string{"CUSTOMER_TOKEN=very-secret-token"})
	require.NotNil(t, masker)
	node := &exec.Node{StatusDetails: []exec.NodeStatusDetail{
		{Label: "customer (TOKEN=very-secret-token)", Status: core.NodeFailed},
	}}

	maskNodeSecrets(masker, node)

	require.Len(t, node.StatusDetails, 1)
	assert.NotContains(t, node.StatusDetails[0].Label, "very-secret-token")
}
