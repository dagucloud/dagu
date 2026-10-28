// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"image"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model is offered what the window's elements say: a field with no name
// of its own by the label beside it, what each holds with the values the
// step types written as their placeholders, and the options of an open menu
// before everything else.
func TestTheModelIsOfferedWhatElementsHold(t *testing.T) {
	t.Parallel()

	run := newTestRun(t)
	run.elements = newFakeElements()
	run.elements.elements = append(run.elements.elements,
		desktop.Element{Role: desktop.RoleTextField, Label: "数量", Value: "12", Bounds: image.Rect(190, 220, 390, 244),
			Path: []desktop.PathStep{{Role: desktop.RoleTextField, Index: 1}}},
		desktop.Element{Role: desktop.RoleMenuItem, Name: "明光ラボ", Bounds: image.Rect(190, 250, 390, 270),
			Path: []desktop.PathStep{{Role: desktop.RoleMenuItem, Name: "明光ラボ", Index: 0}}},
	)
	session := &scriptedSession{turns: []*computeruse.Turn{done("Looked")}}
	run.sessions = []*scriptedSession{session}
	execution := run.execute(`{"idle": "0", "variables": {"qty": "12"}, "do": [{"act": "数量に %qty% を入れる"}]}`, nil)
	require.NoError(t, execution.err)
	require.NotEmpty(t, session.observations)
	offered := session.observations[0].Elements
	require.NotEmpty(t, offered)
	assert.Equal(t, computeruse.Element{ID: "e1", Role: desktop.RoleMenuItem, Name: "明光ラボ"}, offered[0], "the open menu's option comes first")
	assert.Contains(t, offered, computeruse.Element{ID: "e4", Role: desktop.RoleTextField, Name: "数量", Value: "%qty%"},
		"the unnamed field goes by its label, and the typed value by its placeholder")
}
