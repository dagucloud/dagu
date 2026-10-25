// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/desktop"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func stepWith(t *testing.T, withJSON string, llm *ir.LLMConfig) ir.Step {
	t.Helper()
	var with map[string]any
	require.NoError(t, json.Unmarshal([]byte(withJSON), &with))
	return ir.Step{Name: "post", ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: with}, LLM: llm}
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	model := &ir.LLMConfig{Provider: "anthropic", Model: "claude-opus-5"}
	for _, tc := range []struct {
		name string
		with string
		llm  *ir.LLMConfig
		want string
	}{
		{name: "valid", with: `{"do": [{"launch": "notepad.exe"}, {"act": "Type hello"}, {"wait": "2s"}, {"screenshot": "done"}]}`, llm: model},
		{name: "missing model", with: `{"do": [{"act": "Type hello"}]}`, want: "computer actions need a model for do[0] act"},
		{name: "never needs no model", with: `{"ai": "never", "do": [{"launch": "notepad.exe"}, {"act": "Type hello"}, {"wait": "1s"}]}`},
		{name: "act that overrides never needs a model", with: `{"ai": "never", "do": [{"act": {"instruction": "x", "ai": "on_miss"}}]}`, want: "need a model for do[0] act"},
		{name: "never with expect statement", with: `{"ai": "never", "do": [{"expect": "Saved"}]}`, llm: model, want: "do[0]: expect is judged by AI"},
		{name: "never with when statement", with: `{"ai": "never", "do": [{"act": "x", "when": "Saved"}]}`, llm: model, want: "do[0]: when is judged by AI"},
		{name: "never with exact expect needs no model", with: `{"ai": "never", "do": [{"expect": {"text": "保存しました"}}, {"expect": {"window": "経費精算"}}]}`},
		{name: "never with exact when", with: `{"ai": "never", "do": [{"act": "x", "when": {"element": {"role": "button", "name": "保存"}}}]}`},
		{name: "expect statement needs a model", with: `{"do": [{"expect": "Saved"}]}`, want: "computer actions need a model for do[0] expect"},
		{name: "when statement needs a model", with: `{"do": [{"act": {"instruction": "x", "ai": "never"}, "when": "Saved"}]}`, want: "computer actions need a model for do[0] when"},
		{name: "two check keys", with: `{"do": [{"expect": {"text": "Saved", "window": "App"}}]}`, llm: model, want: "oneOf"},
		{name: "statement and text", with: `{"do": [{"expect": {"statement": "Saved", "text": "Saved"}}]}`, llm: model, want: "oneOf"},
		{name: "only within", with: `{"do": [{"expect": {"within": "10s"}}]}`, llm: model, want: "expect"},
		{name: "element without role", with: `{"do": [{"expect": {"element": {"name": "保存"}}}]}`, llm: model, want: "role"},
		{name: "nested container without role", with: `{"do": [{"expect": {"element": {"role": "button", "name": "保存", "in": {"name": "支払情報"}}}}]}`, llm: model, want: "role"},
		{name: "unknown selector key", with: `{"do": [{"expect": {"element": {"role": "button", "title": "保存"}}}]}`, llm: model, want: "title"},
		{name: "element role alone", with: `{"do": [{"expect": {"element": {"role": "button"}}}]}`, llm: model, want: "name"},
		{name: "bad exact within", with: `{"do": [{"expect": {"text": "Saved", "within": "later"}}]}`, llm: model, want: "within"},
		{name: "unknown variable in text", with: `{"do": [{"expect": {"text": "%code%"}}]}`, llm: model, want: "expect references %code%"},
		{name: "unknown variable in selector", with: `{"do": [{"act": "x", "when": {"element": {"role": "text_field", "near": {"label": "%label%", "side": "right"}}}}]}`, llm: model, want: "when references %label%"},
		{name: "variable in a container from an earlier ask", with: `{"do": [{"ask": {"prompt": "Group?", "as": "group"}}, {"expect": {"element": {"role": "button", "name": "保存", "in": {"role": "group", "name": "%group%"}}}}]}`, llm: model},
		{name: "variable in a window from variables", with: `{"variables": {"doc": "1042"}, "do": [{"expect": {"window": "請求書 %doc%"}}]}`, llm: model},
		{name: "never with extract of no bindable field", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "schema": {"type": "object"}}}]}`, llm: model, want: "has no field that reads from an element"},
		{name: "never with extract of a bindable field", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "number"}}}}}]}`, llm: model},
		{name: "never with extract of an object field", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "number"}, "lines": {"type": "object"}}}}}]}`, llm: model, want: `extract under never cannot read lines`},
		{name: "extract that overrides never", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "ai": "on_miss", "schema": {"type": "object"}}}]}`, llm: model},
		{name: "ai and cache", with: `{"ai": "never", "cache": true, "do": [{"act": "x"}]}`, llm: model, want: "set ai or cache, not both"},
		{name: "when on a value needs no model", with: `{"ai": "never", "variables": {"same": "あり"}, "do": [{"wait": "1s", "when": {"value": "%same%", "equals": "あり"}}, {"wait": "1s", "when": {"value": "", "not_equals": ""}}]}`},
		{name: "value without equals", with: `{"do": [{"wait": "1s", "when": {"value": "a"}}]}`, want: "value takes exactly one of equals or not_equals"},
		{name: "value with equals and not_equals", with: `{"do": [{"wait": "1s", "when": {"value": "a", "equals": "a", "not_equals": "b"}}]}`, want: "value takes exactly one of equals or not_equals"},
		{name: "value with within", with: `{"do": [{"wait": "1s", "when": {"value": "a", "equals": "a", "within": "5s"}}]}`, want: "value is decided at once and takes no within"},
		{name: "equals without value", with: `{"do": [{"wait": "1s", "when": {"text": "a", "equals": "a"}}]}`, want: "equals and not_equals go with value"},
		{name: "value with an unknown variable", with: `{"do": [{"wait": "1s", "when": {"value": "%nope%", "equals": "a"}}]}`, want: "when references %nope%"},
		{name: "handler", with: `{"ai": "never", "variables": {"answer": "はい"}, "handle": [{"name": "確認", "when": {"text": "クリアしていいですか"}, "recorded": {"macos": [{"click": {"role": "button", "name": "%answer%"}}]}}], "do": [{"expect": {"window": "受付"}}]}`},
		{name: "handler judged by a statement", with: `{"handle": [{"when": "A confirmation is open", "recorded": {"macos": [{"click": {"role": "button", "name": "はい"}}]}}], "do": [{"act": "x"}]}`, llm: model, want: "/properties/handle/items/properties/when"},
		{name: "handler with within", with: `{"handle": [{"when": {"text": "確認", "within": "5s"}, "recorded": {"macos": [{"click": {"role": "button", "name": "はい"}}]}}], "do": [{"act": "x"}]}`, llm: model, want: `unexpected additional properties ["within"]`},
		{name: "handler without steps", with: `{"handle": [{"when": {"text": "確認"}, "recorded": {}}], "do": [{"act": "x"}]}`, llm: model, want: "handle[0]: recorded must list the steps"},
		{name: "handler with an unknown variable", with: `{"handle": [{"when": {"text": "確認"}, "recorded": {"macos": [{"click": {"role": "button", "name": "%answer%"}}]}}], "do": [{"act": "x"}]}`, llm: model, want: "handle[0] references %answer%"},
		{name: "check a checkbox", with: `{"ai": "never", "do": [{"act": {"instruction": "x", "recorded": {"macos": [{"check": {"role": "checkbox", "id": "agree"}}, {"uncheck": {"role": "checkbox", "id": "news"}}]}}}]}`},
		{name: "check a button", with: `{"ai": "never", "do": [{"act": {"instruction": "x", "recorded": {"macos": [{"check": {"role": "button", "id": "save"}}]}}}]}`, want: "/properties/check"},
		{name: "check with an unknown variable", with: `{"ai": "never", "do": [{"act": {"instruction": "x", "recorded": {"macos": [{"check": {"role": "checkbox", "name": "%kasho%"}}]}}}]}`, want: "act references %kasho%"},
		{name: "maximize on the step and an operation", with: `{"maximize": false, "do": [{"act": {"instruction": "x", "maximize": true}}, {"extract": {"instruction": "a", "maximize": true, "schema": {"type": "object"}}}]}`, llm: model},
		{name: "maximize that is not a boolean", with: `{"maximize": "yes", "do": [{"act": "x"}]}`, llm: model, want: "maximize"},
		{name: "ai and cache on act", with: `{"do": [{"act": {"instruction": "x", "ai": "never", "cache": false}}]}`, llm: model, want: "act: set ai or cache, not both"},
		{name: "unknown ai", with: `{"ai": "sometimes", "do": [{"act": "x"}]}`, llm: model, want: "ai"},
		{name: "find within", with: `{"find_within": "5s", "do": [{"act": {"instruction": "x", "find_within": "0"}}]}`, llm: model},
		{name: "bad find within", with: `{"find_within": "soon", "do": [{"act": "x"}]}`, llm: model, want: `find_within "soon" must be a duration`},
		{name: "negative find within on act", with: `{"do": [{"act": {"instruction": "x", "find_within": "-1s"}}]}`, llm: model, want: `act: find_within "-1s"`},
		{name: "input", with: `{"input": "direct", "do": [{"act": {"instruction": "x", "input": "screen"}}]}`, llm: model},
		{name: "bad input", with: `{"input": "mouse", "do": [{"act": "x"}]}`, llm: model, want: "input"},
		{name: "bad input on act", with: `{"do": [{"act": {"instruction": "x", "input": "keys"}}]}`, llm: model, want: "input"},
		{name: "pinned act", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"click": {"role": "button", "name": "Save"}}, {"key": "ctrl+s"}]}}}]}`, llm: model},
		{name: "pin bad system", with: `{"do": [{"act": {"instruction": "x", "recorded": {"linux": [{"key": "enter"}]}}}]}`, llm: model, want: "linux"},
		{name: "pin two actions", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"key": "enter", "wait": "1s"}]}}}]}`, llm: model, want: "oneOf"},
		{name: "pinned key with focus", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"key": "enter", "focus": {"role": "text_field", "id": "amt"}}]}}}]}`, llm: model},
		{name: "pin focus without key", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"click": {"role": "button", "name": "Save"}, "focus": {"role": "text_field", "id": "amt"}}]}}}]}`, llm: model, want: "focus applies to a key step only"},
		{name: "pin focus bad selector", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"key": "enter", "focus": {"role": "text_field"}}]}}}]}`, llm: model, want: "name"},
		{name: "pin bad selector", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"click": {"role": "button"}}]}}}]}`, llm: model, want: "name"},
		{name: "pin unknown variable", with: `{"do": [{"act": {"instruction": "x", "recorded": {"windows": [{"type": {"into": {"role": "text_field", "id": "amt"}, "text": "%amount%"}}]}}}]}`, llm: model, want: "act references %amount%"},
		{name: "pin variable from variables", with: `{"variables": {"amount": "5"}, "do": [{"act": {"instruction": "x", "recorded": {"windows": [{"type": {"into": {"role": "text_field", "id": "amt"}, "text": "%amount%"}}]}}}]}`, llm: model},
		{name: "pin variable in a selector name from an earlier ask", with: `{"do": [{"ask": {"prompt": "Who?", "as": "who"}}, {"act": {"instruction": "x", "recorded": {"windows": [{"click": {"role": "button", "name": "%who%"}}]}}}]}`, llm: model},
		{name: "pin field not in schema", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "number"}}}, "fields": {"windows": {"amount": {"role": "text", "id": "t"}}}}}]}`, llm: model, want: "not a property of the extract schema"},
		{name: "pin scalar field not bindable", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "object"}}}, "fields": {"windows": {"total": {"role": "text", "id": "t"}}}}}]}`, llm: model, want: "its type is not a string, number"},
		{name: "pin container not a table", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "number"}}}, "fields": {"windows": {"total": {"container": {"role": "group", "id": "g"}, "columns": {"total": "T"}}}}}}]}`, llm: model, want: "is not an array of objects"},
		{name: "pin container without columns", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"rows": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}}}}}}, "fields": {"windows": {"rows": {"container": {"role": "group", "id": "g"}}}}}}]}`, llm: model, want: "names no columns"},
		{name: "pin column not in items", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"rows": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}}}}}}, "fields": {"windows": {"rows": {"container": {"role": "group", "id": "g"}, "columns": {"wrong": "W"}}}}}}]}`, llm: model, want: `column "wrong" is not a property`},
		{name: "pin valid table", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"rows": {"type": "array", "items": {"type": "object", "properties": {"name": {"type": "string"}}}}}}, "fields": {"windows": {"rows": {"container": {"role": "group", "id": "g"}, "columns": {"name": "名前"}}}}}}]}`, llm: model},
		{name: "pin valid scalar field", with: `{"do": [{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {"type": "number"}}}, "fields": {"windows": {"total": {"role": "text", "id": "t"}}}}}]}`, llm: model},
		{name: "empty do", with: `{"do": []}`, llm: model, want: "do"},
		{name: "unknown field", with: `{"url": "https://example.com", "do": [{"act": "x"}]}`, llm: model, want: "url"},
		{name: "two operations", with: `{"do": [{"act": "x", "wait": "1s"}]}`, llm: model, want: "do"},
		{name: "unknown variable", with: `{"do": [{"act": "Type %code%"}]}`, llm: model, want: "act references %code%"},
		{name: "variable from later ask", with: `{"do": [{"act": "Type %code%"}, {"ask": {"prompt": "Code?", "as": "code"}}]}`, llm: model, want: "act references %code%"},
		{name: "variable from earlier ask", with: `{"do": [{"ask": {"prompt": "Code?", "as": "code"}}, {"act": "Type %code%"}]}`, llm: model},
		{name: "ask collides with variable", with: `{"variables": {"code": "1"}, "do": [{"ask": {"prompt": "Code?", "as": "code"}}]}`, llm: model, want: "collides with a variable"},
		{name: "duplicate output", with: `{"do": [
			{"extract": {"instruction": "a", "schema": {"type": "object", "properties": {"total": {}}}}},
			{"extract": {"instruction": "b", "schema": {"type": "object", "properties": {"total": {}}}}}
		]}`, llm: model, want: `output "total" is already extracted by do[0]`},
		{name: "bad wait", with: `{"do": [{"wait": "soon"}]}`, llm: model, want: "must be a positive duration"},
		{name: "bad within", with: `{"do": [{"expect": {"statement": "Saved", "within": "later"}}]}`, llm: model, want: "within"},
		{name: "bad screenshot name", with: `{"do": [{"screenshot": "../x"}]}`, llm: model, want: "screenshot name"},
		{name: "unknown mode", with: `{"mode": "fast", "do": [{"act": "x"}]}`, llm: model, want: "mode"},
		{name: "no idle wait", with: `{"idle": "0", "do": [{"act": "x"}]}`, llm: model},
		{name: "bad idle", with: `{"idle": "soon", "do": [{"act": "x"}]}`, llm: model, want: `idle "soon" must be a duration`},
		{name: "negative idle", with: `{"idle": "-1s", "do": [{"act": "x"}]}`, llm: model, want: `idle "-1s" must be a duration`},
		{name: "native without native provider", with: `{"mode": "native", "do": [{"act": "x"}]}`, llm: &ir.LLMConfig{Provider: "openrouter", Model: "m"}, want: `provider "openrouter" has no native computer use`},
		{name: "native with referenced provider", with: `{"mode": "native", "do": [{"act": "x"}]}`, llm: &ir.LLMConfig{Provider: "${PROVIDER}", Model: "m"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateStep(stepWith(t, tc.with, tc.llm))
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestConfigShorthands(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig(stepWith(t, `{"do": [
		{"launch": {"command": "open", "args": ["-a", "TextEdit"]}},
		{"act": {"instruction": "Type hello", "cache": false, "max_actions": 5}},
		{"expect": {"statement": "Saved", "within": "30s"}}
	]}`, nil).ExecutorConfig.Config)
	require.NoError(t, err)

	assert.Equal(t, launchSpec{Command: "open", Args: []string{"-a", "TextEdit"}}, *cfg.Do[0].Launch)
	assert.Equal(t, 5, cfg.maxActions(*cfg.Do[1].Act))
	assert.Equal(t, defaultMaxActions, cfg.maxActions(actSpec{}))
	wait, err := cfg.findWithin(actSpec{}, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, wait, "the fallback applies when nothing is set")
	wait, err = config{FindWithin: "3s"}.findWithin(actSpec{}, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 3*time.Second, wait)
	wait, err = config{FindWithin: "3s"}.findWithin(actSpec{FindWithin: "0"}, 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), wait, "the act's setting wins")
	_, err = config{FindWithin: "${WAIT}"}.findWithin(actSpec{}, 10*time.Second)
	require.ErrorContains(t, err, "must be a duration")
	direct, err := config{}.direct(actSpec{})
	require.NoError(t, err)
	assert.False(t, direct, "the screen is the default")
	direct, err = config{Input: "direct"}.direct(actSpec{})
	require.NoError(t, err)
	assert.True(t, direct)
	direct, err = config{Input: "direct"}.direct(actSpec{Input: "screen"})
	require.NoError(t, err)
	assert.False(t, direct, "the act's setting wins")
	_, err = config{Input: "mouse"}.direct(actSpec{})
	require.ErrorContains(t, err, "must be screen or direct")
	assert.Equal(t, "Saved", cfg.Do[2].Expect.Statement)
	assert.Equal(t, "30s", cfg.Do[2].Expect.Within)

	exact, err := parseConfig(stepWith(t, `{"do": [
		{"expect": {"element": {"role": "button", "name": "保存", "in": {"role": "group", "name": "支払情報"}}, "within": "5s"}},
		{"expect": {"text": "保存しました"}},
		{"wait": "1s", "when": {"window": "経費精算*"}}
	]}`, nil).ExecutorConfig.Config)
	require.NoError(t, err)
	assert.Equal(t, "button", exact.Do[0].Expect.Element.Role)
	assert.Equal(t, "支払情報", exact.Do[0].Expect.Element.In.Name)
	assert.Equal(t, `element {"role":"button","name":"保存","in":{"role":"group","name":"支払情報"}}`, exact.Do[0].Expect.String())
	assert.Equal(t, `text "保存しました"`, exact.Do[1].Expect.String())
	assert.Equal(t, `window "経費精算*"`, exact.Do[2].When.String())
	assert.False(t, exact.Do[2].When.judged())
	assert.Equal(t, 5*time.Second, exact.Do[0].Expect.window(time.Minute))
	assert.Equal(t, time.Minute, exact.Do[1].Expect.window(time.Minute), "without within the fallback applies")
	require.ErrorContains(t, condition{Text: "a", Window: "b"}.validate(), "exactly one of statement, text, element, window, or value")
	require.ErrorContains(t, condition{}.validate(), "exactly one of")
}

// Placeholders in an exact check are replaced from the variables, in every
// text a selector carries, without touching the original.
func TestSubstituteSelector(t *testing.T) {
	t.Parallel()

	nth := 1
	sel := desktop.Selector{
		Role: "text_field", Name: "請求書 %doc%", Window: "%app% - *", ID: "%doc%", App: "%app%",
		Near: &desktop.Near{Label: "%label%", Side: "right"},
		In:   &desktop.Selector{Role: "group", Name: "%section%"},
		Nth:  &nth,
	}
	values := map[string]string{"doc": "1042", "app": "Expense", "label": "金額", "section": "支払情報"}
	out := substituteSelector(sel, values)

	assert.Equal(t, "請求書 1042", out.Name)
	assert.Equal(t, "Expense - *", out.Window)
	assert.Equal(t, "%doc%", out.ID, "an id is an identifier, not text")
	assert.Equal(t, "%app%", out.App)
	assert.Equal(t, "金額", out.Near.Label)
	assert.Equal(t, "支払情報", out.In.Name)
	assert.Equal(t, 1, *out.Nth)
	assert.Equal(t, "請求書 %doc%", sel.Name, "the original is untouched")
	assert.Equal(t, "%label%", sel.Near.Label)
	assert.Equal(t, "%section%", sel.In.Name)

	c := condition{Element: &sel}
	assert.ElementsMatch(t, []string{"doc", "app", "label", "section"}, c.references())
	assert.Empty(t, condition{Statement: "Type %code%"}.references(), "a statement's placeholders are the model's")
	assert.Equal(t, condition{Statement: "Type %code%"}, condition{Statement: "Type %code%"}.substituted(values))
}

// An act's own choice wins over the step's, and cache: false on either
// still means every run.
func TestChoice(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ with, step, act string }{
		{`{"do": [{"act": "x"}]}`, aiOnMiss, aiOnMiss},
		{`{"cache": false, "do": [{"act": "x"}]}`, aiEveryRun, aiEveryRun},
		{`{"ai": "never", "do": [{"act": {"instruction": "x", "cache": false}}]}`, aiNever, aiEveryRun},
		{`{"cache": false, "do": [{"act": {"instruction": "x", "ai": "never"}}]}`, aiEveryRun, aiNever},
		{`{"ai": "every_run", "do": [{"act": {"instruction": "x", "cache": true}}]}`, aiEveryRun, aiEveryRun},
	} {
		cfg, err := parseConfig(stepWith(t, tc.with, nil).ExecutorConfig.Config)
		require.NoError(t, err, tc.with)
		assert.Equal(t, tc.step, cfg.choice(), tc.with)
		assert.Equal(t, tc.act, cfg.actChoice(*cfg.Do[0].Act), tc.with)
	}
}
