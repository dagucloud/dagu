// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstep_test

import (
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only answers from the session's current generation that the step has not
// applied yet are pending.
func TestPendingAnswer(t *testing.T) {
	t.Parallel()

	deadline := time.Now().Add(time.Hour)
	old := agentstep.AskInteraction(0, 1, "Input", "Code?", deadline)
	old.Status = ir.AgentInteractionAnswered
	current := agentstep.AskInteraction(2, 2, "Input", "Code?", deadline)
	session := &ir.AgentSession{Provider: "computer", Generation: 2, Interactions: []ir.AgentInteraction{old, current}}

	_, ok := agentstep.PendingAnswer(session, "computer")
	assert.False(t, ok, "the current ask is still unanswered")

	session.Interactions[1].Status = ir.AgentInteractionAnswered
	session.Interactions[1].Answers = [][]string{{"731902"}}
	answer, ok := agentstep.PendingAnswer(session, "computer")
	require.True(t, ok)
	assert.Equal(t, agentstep.AskAnswer{InteractionID: current.ID}, answer)
	assert.Equal(t, map[int]string{2: "731902"}, agentstep.AnsweredAsks(session))

	_, ok = agentstep.PendingAnswer(session, "browser")
	assert.False(t, ok, "another provider's session")

	agentstep.MarkApplied(session, current.ID)
	_, ok = agentstep.PendingAnswer(session, "computer")
	assert.False(t, ok, "an applied answer is not pending")

	session.Interactions[1].Applied = false
	session.Interactions[1].Status = ir.AgentInteractionRejected
	answer, ok = agentstep.PendingAnswer(session, "computer")
	require.True(t, ok)
	assert.True(t, answer.Rejected)
}

func TestCheckSecrets(t *testing.T) {
	t.Parallel()

	secrets := map[string]string{"TOKEN": "tok-12345", "PIN": "42"}
	operations := []agentstep.OperationTexts{
		{Kind: "act", Texts: []string{"Log in with %token%"}},
		{Kind: "extract", Texts: []string{"Read the value 42 and tok-12345"}},
	}
	err := agentstep.CheckSecrets("computer", operations, secrets)
	require.EqualError(t, err, "computer: do[1].extract contains the value of secret TOKEN, which would be sent to the model; pass it in with.variables and reference it as %name%")
	require.NoError(t, agentstep.CheckSecrets("computer", operations[:1], secrets), "short secrets such as PIN are not checked")

	masker := agentstep.NewMasker(secrets, map[string]string{"otp": "731902"})
	assert.NotContains(t, masker.MaskString("tok-12345 and 731902"), "tok-12345")
	assert.NotContains(t, masker.MaskString("tok-12345 and 731902"), "731902")
	assert.Contains(t, masker.MaskString("pin 42"), "42", "values shorter than four characters are not masked")
}
