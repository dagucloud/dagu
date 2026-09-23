// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DAG_PUSHBACK is one environment variable, so a long review loop must not
// grow it past the platform limit. The newest history entries are kept.
func TestMarshalPushBackPayloadTrimsOldestHistory(t *testing.T) {
	t.Parallel()

	var history []ir.PushBackEntry
	for i := 1; i <= 12; i++ {
		history = append(history, ir.PushBackEntry{
			Iteration: i,
			By:        "reviewer",
			Inputs:    map[string]string{"feedback": strings.Repeat("x", 4<<10)},
		})
	}
	latest := history[len(history)-1].Inputs
	state := NodeState{ApprovalIteration: 12, PushBackInputs: latest, PushBackHistory: history}

	payload, err := marshalPushBackPayload(nil, state)
	require.NoError(t, err)
	require.LessOrEqual(t, len(payload), maxPushBackPayloadSize)

	var decoded pushBackPayload
	require.NoError(t, json.Unmarshal([]byte(payload), &decoded))
	assert.Equal(t, 12, decoded.Iteration)
	assert.Equal(t, "reviewer", decoded.By)
	assert.Equal(t, latest, decoded.Inputs)
	require.NotEmpty(t, decoded.History)
	require.Less(t, len(decoded.History), len(history))
	assert.Equal(t, 12, decoded.History[len(decoded.History)-1].Iteration)
	assert.Equal(t, 13-len(decoded.History), decoded.History[0].Iteration)
}
