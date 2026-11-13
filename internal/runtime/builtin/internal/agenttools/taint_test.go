// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agenttools

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A tool DAG of an agent step runs trusted. An input of the model in a
// reference that Dagu substitutes would put the text of the model in a shell.
func TestCheckSubstitutedInputs(t *testing.T) {
	t.Parallel()
	inputs := []toolParam{{Name: "CUSTOMER_ID"}}
	dagWith := func(step ir.Step) *ir.DAG { return &ir.DAG{Name: "lookup", Steps: []ir.Step{step}} }

	for _, text := range []string{"lookup ${params.CUSTOMER_ID}", "lookup ${env.CUSTOMER_ID}", "lookup ${ params.CUSTOMER_ID }"} {
		err := checkSubstitutedInputs(dagWith(ir.Step{Name: "s", CmdWithArgs: text}), inputs)
		require.Error(t, err, text)
		assert.Contains(t, err.Error(), `"$CUSTOMER_ID"`)
	}
	err := checkSubstitutedInputs(dagWith(ir.Step{Name: "s", Script: "curl x/${params.CUSTOMER_ID}"}), inputs)
	require.Error(t, err, "a script is a command text")
	err = checkSubstitutedInputs(dagWith(ir.Step{Name: "s", Commands: []ir.CommandEntry{{Command: "lookup", Args: []string{"${params.CUSTOMER_ID}"}}}}), inputs)
	require.Error(t, err, "an argument of a command entry is a command text")
	err = checkSubstitutedInputs(&ir.DAG{Name: "lookup", HandlerOn: ir.HandlerOn{Exit: &ir.Step{Name: "exit", CmdWithArgs: "echo ${params.CUSTOMER_ID}"}}}, inputs)
	require.Error(t, err, "a handler step is a command text")

	// The shell reads these forms, so the value stays data.
	for _, text := range []string{`lookup "$CUSTOMER_ID"`, `lookup "${CUSTOMER_ID}"`} {
		assert.NoError(t, checkSubstitutedInputs(dagWith(ir.Step{Name: "s", CmdWithArgs: text}), inputs), text)
	}
	// A fixed input is not an input of the model.
	assert.NoError(t, checkSubstitutedInputs(dagWith(ir.Step{Name: "s", CmdWithArgs: "lookup ${params.REGION}"}), inputs))
}
