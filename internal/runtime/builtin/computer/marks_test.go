// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"errors"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/llm/computeruse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markedRun is a run whose marks show among its input, with the keyboard
// focus on the expense form, so each mark's place before its action shows.
func markedRun(t *testing.T) *testRun {
	t.Helper()
	run := elementRun(t)
	run.backend.focusOn(desktop.WindowID{Handle: 7, Title: expenseWindow().Name, App: "expense", Bounds: expenseWindow().Bounds})
	run.marks.onShow = func(m desktop.Mark) { run.backend.recordAccessibility("mark " + m.Label) }
	run.sessions = []*scriptedSession{{turns: []*computeruse.Turn{actions(clickAt(235, 194), typing("12,000")), done("Saved")}}}
	return run
}

// On the console, a step marks where each action lands just before it
// acts, labelled with the step's name and the action: the point a click
// lands on, the window that takes typing, and an element that performs a
// pattern itself, which moves no pointer. No mark shows what is typed.
func TestMarksShowWhereTheStepActs(t *testing.T) {
	t.Parallel()

	const steps = `{"do": [{"act": "Save the invoice"}]}`
	run := markedRun(t)
	require.NoError(t, run.execute(steps, nil).err)
	assert.Equal(t, []string{"mark post · click", "move 235,194", "left down #1", "mark post · type", "type 12,000"}, run.backend.inputs())
	assert.Equal(t, []string{"post · click (211,170)-(259,218)", "post · type (100,100)-(700,500)"}, run.marks.marked())
	assert.True(t, run.marks.closed, "the step closes its marks")

	run.backend.events = nil
	run.marks.shown = nil
	require.NoError(t, run.execute(`{"input": "direct", "do": [{"act": "Save the invoice"}]}`, nil).err)
	assert.Equal(t, []string{"mark post · press", "invoke saveButton", "mark post · type", "type 12,000"}, run.backend.inputs())
	assert.Equal(t, "post · press (190,180)-(280,208)", run.marks.marked()[0], "the mark covers the button")
}

// with.marks turns marks off on the console, and on in a remote session,
// which shows none unless it is set.
func TestMarksSetting(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		remote bool
		marks  string
		shown  bool
	}{
		{"console", false, "", true},
		{"console with marks off", false, `"marks": false, `, false},
		{"remote session", true, "", false},
		{"remote session with marks on", true, `"marks": true, `, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := markedRun(t)
			run.remote = tc.remote
			require.NoError(t, run.execute(`{`+tc.marks+`"do": [{"act": "Save the invoice"}]}`, nil).err)
			assert.Equal(t, tc.shown, len(run.marks.marked()) > 0)
		})
	}
}

// A host that cannot draw marks runs the step without them, and says why.
func TestMarksUnavailable(t *testing.T) {
	t.Parallel()

	run := markedRun(t)
	run.marksErr = errors.New("this version of Windows cannot leave marks out of screen captures")
	execution := run.execute(`{"do": [{"act": "Save the invoice"}]}`, nil)
	require.NoError(t, execution.err)
	assert.Equal(t, []string{"move 235,194", "left down #1", "type 12,000"}, run.backend.inputs())
	assert.Contains(t, execution.stderr.String(), "warning: show where the step acts: this version of Windows cannot leave marks out of screen captures")
}
