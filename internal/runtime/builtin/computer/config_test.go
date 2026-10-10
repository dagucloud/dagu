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
		{name: "never with extract", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "schema": {"type": "object"}}}]}`, llm: model, want: "do[0]: extract needs AI"},
		{name: "extract that overrides never", with: `{"ai": "never", "do": [{"extract": {"instruction": "a", "ai": "on_miss", "schema": {"type": "object"}}}]}`, llm: model},
		{name: "ai and cache", with: `{"ai": "never", "cache": true, "do": [{"act": "x"}]}`, llm: model, want: "set ai or cache, not both"},
		{name: "ai and cache on act", with: `{"do": [{"act": {"instruction": "x", "ai": "never", "cache": false}}]}`, llm: model, want: "act: set ai or cache, not both"},
		{name: "unknown ai", with: `{"ai": "sometimes", "do": [{"act": "x"}]}`, llm: model, want: "ai"},
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
	require.ErrorContains(t, condition{Text: "a", Window: "b"}.validate(), "exactly one of statement, text, element, or window")
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
