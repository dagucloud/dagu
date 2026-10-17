// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package ir

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTriggerTypeRoundTrip(t *testing.T) {
	t.Parallel()

	for _, triggerType := range []TriggerType{
		TriggerTypeScheduler,
		TriggerTypeManual,
		TriggerTypeWebhook,
		TriggerTypeSubDAG,
		TriggerTypeRetry,
		TriggerTypeCatchUp,
		TriggerTypeRestart,
	} {
		assert.Equal(t, triggerType, ParseTriggerType(triggerType.String()), triggerType.String())
	}
	assert.Equal(t, "restart", TriggerTypeRestart.String())
	assert.Equal(t, TriggerTypeUnknown, ParseTriggerType("bogus"))
}
