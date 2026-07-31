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
