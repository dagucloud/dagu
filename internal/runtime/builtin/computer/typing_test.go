// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The model is told what its typing did to the field: what the field now
// shows, or, when the field shows what it did before without the typed
// text, that the typing did not reach it, which stops the turn before the
// save after it. A field that shows the typed value as it formats it, such
// as 12,000 for 12000, has it.
func TestTheModelIsToldWhatTypingDid(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		typed string
		lands bool
		check func(t *testing.T, results []computeruse.Result, inputs []string)
	}{
		{"it lands", "1200", true, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.Equal(t, `the field now shows "1200"`, results[0].Output)
			assert.Contains(t, inputs, "invoke saveButton", "the save after it runs")
		}},
		{"it does not land", "1200", false, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.Equal(t, `the typing did not reach the field, which still shows "12,000"`, results[0].Error)
			assert.True(t, results[1].Skipped, "the save after it does not run")
			assert.NotContains(t, inputs, "invoke saveButton")
		}},
		{"the field shows it formatted", "12000", false, func(t *testing.T, results []computeruse.Result, inputs []string) {
			assert.False(t, results[0].Failed(), results[0].Error)
			assert.Contains(t, inputs, "invoke saveButton", "the save after it runs")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := elementRun(t)
			run.focusOn("amountBox")
			if tc.lands {
				run.backend.onType = func(text string) {
					run.elements.mu.Lock()
					run.elements.focused.Value = text
					run.elements.mu.Unlock()
				}
			}
			session := &scriptedSession{turns: []*computeruse.Turn{
				actions(typing(tc.typed), computeruse.Action{CallID: "s", Kind: computeruse.KindClick, ElementID: "e1"}),
				done("Entered"),
			}}
			run.sessions = []*scriptedSession{session}
			execution := run.execute(`{"idle": "0", "do": [{"act": "金額に 1200 を入れて保存する"}]}`, nil)
			require.NoError(t, execution.err)
			require.Len(t, session.observations, 2)
			tc.check(t, session.observations[1].Results, run.backend.inputs())
		})
	}
}

// A field that shows nothing before and after the typing, as a password
// field always does, says nothing of it.
func TestTypingIntoAFieldThatShowsNothingSaysNothing(t *testing.T) {
	t.Parallel()

	run := elementRun(t)
	run.elements.elements = append(run.elements.elements, desktop.Element{Role: desktop.RoleTextField, ID: "pinBox",
		Path: []desktop.PathStep{{Role: desktop.RoleTextField, Index: 1}}})
	run.focusOn("pinBox")
	session := &scriptedSession{turns: []*computeruse.Turn{actions(typing("4321")), done("Entered")}}
	run.sessions = []*scriptedSession{session}
	require.NoError(t, run.execute(`{"idle": "0", "do": [{"act": "暗証番号を入れる"}]}`, nil).err)
	require.Len(t, session.observations, 2)
	result := session.observations[1].Results[0]
	assert.False(t, result.Failed())
	assert.Empty(t, result.Output)
}
