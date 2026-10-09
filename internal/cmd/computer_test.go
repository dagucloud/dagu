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
	if goruntime.GOOS == "windows" {
		t.Skip("the result depends on the desktop session")
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
	assert.Contains(t, []string{"unsupported", "no_session", "other_session", "screen_locked", "no_display", "screen_recording", "accessibility", "load_failed"}, result.Error.Code)
	if goruntime.GOOS != "darwin" {
		assert.Equal(t, "unsupported", result.Error.Code)
	}
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
