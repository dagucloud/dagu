// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputerCacheClear(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	seedComputerReplayCache(t, th, "invoices", "post", "read")
	seedBrowserReplayCache(t, th, "invoices", "post")

	out, err := runCommand(th, cmd.Computer(), "computer", "cache", "clear", "invoices", "--step", "post")
	require.NoError(t, err)
	assert.Equal(t, "Removed computer replay cache for step \"post\" of DAG \"invoices\"\n", out)
	assert.Equal(t, []string{"read"}, computerReplayCacheSteps(t, th, "invoices"))
	assert.Equal(t, []string{"post"}, browserReplayCacheSteps(t, th, "invoices"), "browser steps keep their cache")
}

// The recordings of a step show as the pins to write into it, by task and
// newest first, and an act that cannot be pinned names each action and why.
func TestComputerCacheShow(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	writeComputerRecordings(t, th, "invoices", "post", map[string]string{
		"older": `{"op": 0, "version": 5, "system": "windows", "saved_at": "2026-10-01T09:00:00Z", "turns": [{"actions": [{"action": {"kind": "key", "keys": ["ctrl", "s"]}}]}]}`,
		"newer": `{"op": 0, "version": 5, "system": "windows", "saved_at": "2026-10-02T09:00:00Z", "turns": [{"actions": [{"action": {"kind": "click", "point": {"x": 1, "y": 2}}, "element": {"selector": {"role": "button", "name": "保存", "id": "saveButton"}, "path": [], "app": "expense", "window": "経費精算", "fx": 0.5, "fy": 0.5}}]}]}`,
		"drag":  `{"op": 1, "version": 5, "system": "windows", "turns": [{"actions": [{"action": {"kind": "drag", "path": [{"x": 1, "y": 2}, {"x": 3, "y": 4}]}}]}]}`,
		"read":  `{"op": 2, "version": 2, "system": "windows", "fields": [{"name": "amount", "kind": "number", "element": {"selector": {"role": "text_field", "id": "amountBox"}, "path": [], "app": "expense", "window": "経費精算", "fx": 0, "fy": 0}}]}`,
		"stale": `{"op": 0, "version": 1, "turns": [{"actions": [{"action": {"kind": "key", "keys": ["enter"]}}]}]}`,
	})
	writeComputerRecordings(t, th, "invoices", "check", map[string]string{
		"enter": `{"op": 0, "version": 5, "system": "windows", "turns": [{"actions": [{"action": {"kind": "key", "keys": ["enter"]}}]}]}`,
	})

	out, err := runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "post", "--format", "json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"operations": [
		{"step": "post", "op": 1, "kind": "act", "system": "windows", "saved_at": "2026-10-02T09:00:00Z",
		 "recorded": [{"click": {"app": "expense", "role": "button", "name": "保存", "id": "saveButton"}}]},
		{"step": "post", "op": 1, "kind": "act", "system": "windows", "saved_at": "2026-10-01T09:00:00Z",
		 "recorded": [{"key": "ctrl+s"}]},
		{"step": "post", "op": 2, "kind": "act", "system": "windows",
		 "unpinnable": [{"turn": 1, "action": "drag from 1,2 to 3,4", "code": "drag", "reason": "a drag has no pinned form"}]},
		{"step": "post", "op": 3, "kind": "extract", "system": "windows",
		 "fields": {"amount": {"app": "expense", "role": "text_field", "id": "amountBox"}}}
	]}`, out)

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "post", "--op", "1")
	require.NoError(t, err)
	assert.Equal(t, `Step "post", task 1 (act), recorded 2026-10-02T09:00:00Z:
recorded:
  windows:
  - click:
      app: expense
      role: button
      name: 保存
      id: saveButton

Step "post", task 1 (act), recorded 2026-10-01T09:00:00Z:
recorded:
  windows:
  - key: ctrl+s
`, out)

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "post", "--op", "2")
	require.NoError(t, err)
	assert.Equal(t, "Step \"post\", task 2 (act), cannot be pinned:\n  turn 1, drag from 1,2 to 3,4: a drag has no pinned form\n", out)

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--format", "json")
	require.NoError(t, err)
	var all struct {
		Operations []struct {
			Step string `json:"step"`
			Op   int    `json:"op"`
		} `json:"operations"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &all))
	require.Len(t, all.Operations, 5, "every step without --step")
	assert.Equal(t, "check", all.Operations[0].Step, "steps in order")

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "missing")
	require.NoError(t, err)
	assert.Equal(t, "No computer recordings for step \"missing\" of DAG \"invoices\"\n", out)
}

// A recording that never used a variable its task names, as one made where
// a field already showed the value, says so beside its pin, since the pinned
// steps would leave that field alone for every value. One recorded before
// recordings noted such variables says to check its steps.
func TestComputerCacheShowNamesWhatAPinLeavesAlone(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	writeComputerRecordings(t, th, "invoices", "post", map[string]string{
		"save": `{"op": 0, "version": 5, "system": "macos", "assumed": {"salt": "s", "digests": {"qty": "d1", "item": "d2"}}, "turns": [{"actions": [{"action": {"kind": "key", "keys": ["cmd", "s"]}}]}]}`,
	})
	writeComputerRecordings(t, th, "invoices", "check", map[string]string{
		"enter": `{"op": 0, "version": 4, "system": "macos", "turns": [{"actions": [{"action": {"kind": "key", "keys": ["enter"]}}]}]}`,
	})

	out, err := runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "post", "--format", "json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"operations": [
		{"step": "post", "op": 1, "kind": "act", "system": "macos", "recorded": [{"key": "cmd+s"}], "assumes": ["item", "qty"]}
	]}`, out)

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "post")
	require.NoError(t, err)
	assert.Equal(t, `Step "post", task 1 (act):
# The steps never use %item%, %qty%, which the task names; pinned, they leave each as a run finds it, whatever its value.
recorded:
  macos:
  - key: cmd+s
`, out)

	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "check", "--format", "json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"operations": [
		{"step": "check", "op": 1, "kind": "act", "system": "macos", "recorded": [{"key": "enter"}], "assumes_unknown": true}
	]}`, out)
	out, err = runCommand(th, cmd.Computer(), "computer", "cache", "show", "invoices", "--step", "check")
	require.NoError(t, err)
	assert.Equal(t, `Step "check", task 1 (act):
# Recorded before recordings noted the variables they never use; check that the steps use each variable the task names.
recorded:
  macos:
  - key: enter
`, out)
}

func TestComputerCacheShowInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--op", "1"}, "--op needs --step"},
		{[]string{"--step", "post", "--op", "0"}, `--op must be a position in with.do from 1, not "0"`},
		{[]string{"--format", "yaml"}, `invalid format "yaml": use text or json`},
	} {
		th := test.SetupCommand(t)
		_, err := runCommand(th, cmd.Computer(), append([]string{"computer", "cache", "show", "invoices"}, tc.args...)...)
		require.ErrorContains(t, err, tc.want)
	}
}

// Where the desktop cannot be automated, the JSON result names each problem
// with a code a program can act on.
func TestComputerCheckJSON(t *testing.T) {
	if goruntime.GOOS == "darwin" || goruntime.GOOS == "windows" {
		t.Skip("the result depends on the desktop session")
	}
	t.Parallel()

	th := test.SetupCommand(t)
	out, err := runCommand(th, cmd.Computer(), "computer", "check", "--format", "json")
	require.Error(t, err)

	var result struct {
		OS       string `json:"os"`
		Ready    bool   `json:"ready"`
		Problems []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"problems"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	var shape map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &shape))
	assert.Contains(t, shape, "width", "the result has the same keys when the display is unknown")
	assert.Contains(t, shape, "height")
	assert.Equal(t, goruntime.GOOS, result.OS)
	assert.False(t, result.Ready)
	require.Len(t, result.Problems, 1)
	assert.Equal(t, "unsupported", result.Problems[0].Code)
	assert.Contains(t, result.Problems[0].Message, "macOS and Windows only")
}

// Flags that cannot run are refused before the desktop is touched, as the
// JSON error object when asked.
func TestComputerElementsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"format", []string{"--format", "yaml"}, `--format "yaml": use text or json`},
		{"limit", []string{"--format", "json", "--limit", "0"}, `--limit "0": use a positive number`},
		{"two modes", []string{"--format", "json", "--at-pointer", "--watch"}, "use one of --at-pointer, --watch, or --match"},
		{"after alone", []string{"--format", "json", "--after", "1s"}, "--after needs --at-pointer"},
		{"bad after", []string{"--format", "json", "--at-pointer", "--after", "soon"}, `--after "soon": use a duration such as 3s`},
		{"bad selector json", []string{"--format", "json", "--match", "{"}, "--match: selector:"},
		{"bad selector", []string{"--format", "json", "--match", `{"role": "knob", "name": "x"}`}, `unknown role "knob"`},
		{"selector without name", []string{"--format", "json", "--match", `{"role": "button"}`}, "set name, id, or near"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			th := test.SetupCommand(t)
			out, err := runCommand(th, cmd.Computer(), append([]string{"computer", "elements"}, tc.args...)...)
			require.ErrorContains(t, err, tc.want)
			if tc.name == "format" {
				assert.Empty(t, out, "a text-mode failure prints nothing on stdout")
				return
			}
			var result struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result), out)
			assert.Equal(t, "invalid_input", result.Error.Code)
			assert.Contains(t, result.Error.Message, tc.want)
		})
	}
}

// A system that cannot read elements says so with a code.
func TestComputerElementsUnsupported(t *testing.T) {
	if goruntime.GOOS == "windows" || goruntime.GOOS == "darwin" {
		t.Skip("macOS and Windows read elements; the result depends on the desktop session")
	}
	t.Parallel()

	th := test.SetupCommand(t)
	out, err := runCommand(th, cmd.Computer(), "computer", "elements", "--format", "json")
	require.Error(t, err)
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &result), out)
	assert.Equal(t, "unsupported", result.Error.Code)
}

func computerReplayCache(th test.Command) *replaycache.Store {
	return replaycache.New(filepath.Join(th.Config.Paths.DataDir, computerhost.DataDirName))
}

func seedComputerReplayCache(t *testing.T, th test.Command, dagName string, steps ...string) {
	t.Helper()
	cache := computerReplayCache(th)
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func computerReplayCacheSteps(t *testing.T, th test.Command, dagName string) []string {
	t.Helper()
	steps, err := computerReplayCache(th).Steps(dagName)
	require.NoError(t, err)
	return steps
}

// writeComputerRecordings writes a step's replay cache file holding the
// given recordings by key.
func writeComputerRecordings(t *testing.T, th test.Command, dagName, step string, entries map[string]string) {
	t.Helper()
	raw := make(map[string]json.RawMessage, len(entries))
	for key, entry := range entries {
		raw[key] = json.RawMessage(entry)
	}
	data, err := json.Marshal(raw)
	require.NoError(t, err)
	path := computerReplayCache(th).Path(dagName, step)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}
