// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// signInPage is a sign-in page's tree, as the outline numbers its elements.
var signInPage = pageSnapshot{
	Tree: "[0-1] RootWebArea: Sign in\n  [0-5] textbox: Password\n  [0-6] button: Sign in\n  [0-7] combobox: 状態\n    [0-8] option: すべて [selected]\n    [0-9] option: 未出荷\n  [0-10] button\n",
	XPaths: map[string]string{
		"0-5": "/html[1]/body[1]/input[1]", "0-6": "/html[1]/body[1]/button[1]", "0-7": "/html[1]/body[1]/select[1]", "0-10": "/html[1]/body[1]/button[2]",
	},
}

// An element the outline numbers is acted on by its ID without the model.
// The session keeps each as the act a step writes, with the action it took
// as the act's recording, so the exported step replays it.
func TestSessionActsOnAnElementByID(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = signInPage
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})

	for _, tc := range []struct{ input, act, method, argument string }{
		{`{"type": {"into": "0-5", "text": "%user%"}, "variables": {"user": "alice"}}`, `Type %user% into the "Password" field`, "fill", "%user%"},
		{`{"select": {"in": "0-7", "option": "未出荷"}}`, `Select "未出荷" in the "状態" dropdown`, "selectOptionFromDropdown", "未出荷"},
		{`{"click": "[0-6]"}`, `Click the "Sign in" button`, "click", ""},
	} {
		result, err := ts.do(opened.ID, tc.input)
		require.NoError(t, err, tc.input)
		assert.Equal(t, tc.act, result.Act)
		assert.Equal(t, OperationDone, result.Status)
		assert.True(t, result.Recorded, "the act's action is its recording")
		replayed := ts.engine.replays[len(ts.engine.replays)-1]
		assert.Equal(t, tc.method, replayed.Method)
		if tc.argument != "" {
			assert.Equal(t, []string{tc.argument}, replayed.Arguments)
		}
	}
	assert.Equal(t, "xpath=/html[1]/body[1]/button[1]", ts.engine.replays[2].Selector)
	assert.Zero(t, actRequests(ts.provider), "no element operation asks the model")

	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 3, exported.Recordings)
	assert.JSONEq(t, `{"act":"Click the \"Sign in\" button"}`, string(exported.Step.With.Do[2]))
}

// An element operation names an element the page shows, and one it can do.
func TestSessionRefusesAnElementItCannotUse(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = signInPage
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})
	for input, message := range map[string]string{
		`{"click": "0-99"}`:                               "element 0-99 is not on the page now",
		`{"type": {"into": "0-6", "text": "alice"}}`:      "element 0-6 is a button, which takes no typing",
		`{"select": {"in": "0-5", "option": "x"}}`:        "element 0-5 is a textbox, which has no options",
		`{"click": "0-10"}`:                               "element 0-10 has no name a step could find it by again",
		`{"type": {"into": "0-5", "text": "%password%"}}`: "the instruction uses %password%, which no variable sets",
		`{"select": {"in": "0-7"}}`:                       "select needs the option to pick",
		`{"click": {"element": "0-6"}}`:                   "the element ID must be text",
	} {
		_, err := ts.do(opened.ID, input)
		assert.Equal(t, CodeInvalidInput, sessionCode(err), input)
		assert.ErrorContains(t, err, message, input)
	}
	assert.Empty(t, ts.engine.replays, "nothing reached the page")
	summaries, err := ts.sessions.List(ts.context())
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	assert.Equal(t, SessionIdle, summaries[0].State, "a refused command leaves the session waiting for the next")
}

// An element operation may carry when and timeout, which its act keeps.
func TestSessionElementOperationKeepsItsGuards(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	ts.engine.snapshot = signInPage
	ts.engine.pageText = "Sign in"
	opened := ts.open(SessionOptions{URL: "https://portal.example.com/login", LLM: testModel})

	result, err := ts.do(opened.ID, `{"click": "0-6", "when": {"text": "Sign in"}, "timeout": "5s"}`)
	require.NoError(t, err)
	assert.Equal(t, OperationDone, result.Status)
	exported, err := ts.sessions.Export(ts.context(), ExportRequest{ID: opened.ID, DAG: "orders", Step: "shop", DryRun: true})
	require.NoError(t, err)
	assert.JSONEq(t, `{"act":"Click the \"Sign in\" button","when":{"text":"Sign in"},"timeout":"5s"}`, string(exported.Step.With.Do[0]))

	_, err = ts.do(opened.ID, `{"click": "0-6", "timeout": "soon"}`)
	assert.Equal(t, CodeInvalidInput, sessionCode(err), "its guards are checked as an act's")
}

// A control a form names only by its table row, or by its name attribute,
// is acted on by its ID, and the act a step writes names it so.
func TestSessionActsOnARowNamedElement(t *testing.T) {
	t.Parallel()

	ts := newTestSessions(t, pageModel(nil))
	snap := fixtureSnapshot(t, "rowform")
	snap.Attributes = rowFormAttributes
	snap.XPaths = map[string]string{
		"1-9":   "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[1]/td[1]/select[1]",
		"1-12":  "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[3]/td[1]/input[1]",
		"1-103": "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[4]/td[2]/input[1]",
		"1-17":  "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[5]/td[1]/input[2]",
		"1-13":  "/html[1]/frameset[1]/frame[2]/html[1]/body[1]/form[1]/table[1]/tbody[1]/tr[7]/td[1]/input[1]",
	}
	ts.engine.snapshot = snap
	opened := ts.open(SessionOptions{URL: "https://bids.example.com/", LLM: testModel})

	for _, tc := range []struct{ input, act string }{
		{`{"select": {"in": "1-9", "option": "土木一式工事"}}`, `Select "土木一式工事" in the "工種又は業種" dropdown`},
		{`{"type": {"into": "1-12", "text": "道路"}}`, `Type 道路 into the "案件名" field`},
		{`{"click": "1-103"}`, `Click the "添付あり" checkbox`},
		{`{"type": {"into": "1-17", "text": "2026/04/30"}}`, `Type 2026/04/30 into the "入札予定日範囲 2" field`},
		{`{"type": {"into": "1-13", "text": "舗装"}}`, `Type 舗装 into the "FreeWord" field`},
	} {
		result, err := ts.do(opened.ID, tc.input)
		require.NoError(t, err, tc.input)
		assert.Equal(t, tc.act, result.Act, tc.input)
		assert.True(t, result.Recorded, tc.input)
	}
	assert.Zero(t, actRequests(ts.provider), "no element operation asks the model")
}
