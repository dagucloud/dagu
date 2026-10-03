// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/require"
)

func TestCheckTextSecrets(t *testing.T) {
	t.Parallel()
	secrets := map[string]string{"SHOP_TOKEN": "tok-12345", "PIN": "123", "ZONE": "west-coast"}

	err := agentstep.CheckTextSecrets("xlsx", "instruction", "Use tok-12345 to find the total", secrets)
	require.EqualError(t, err, "xlsx: with.instruction contains the value of secret SHOP_TOKEN, which would be sent to the model")

	require.NoError(t, agentstep.CheckTextSecrets("xlsx", "instruction", "Find the pin 123", secrets),
		"a secret shorter than four characters is not checked")
	require.NoError(t, agentstep.CheckTextSecrets("xlsx", "instruction", "Find the total", secrets))
	require.NoError(t, agentstep.CheckTextSecrets("xlsx", "instruction", "", nil))

	err = agentstep.CheckTextSecrets("xlsx", "instruction", "west-coast tok-12345", secrets)
	require.ErrorContains(t, err, "secret SHOP_TOKEN", "secrets are reported in name order")

	err = agentstep.CheckTextSecrets("xlsx", "instruction", "Use tok-12345", map[string]string{"": "tok-12345"})
	require.EqualError(t, err, "xlsx: with.instruction contains the value of secret , which would be sent to the model",
		"a secret with an empty name is still a secret")
	err = agentstep.CheckSecrets("browser", []agentstep.OperationTexts{{Kind: "act", Texts: []string{"Use tok-12345"}}}, map[string]string{"": "tok-12345"})
	require.ErrorContains(t, err, "contains the value of secret ,")
}
