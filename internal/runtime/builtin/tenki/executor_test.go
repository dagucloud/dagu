// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tenki

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTenki(t *testing.T) {
	tests := []struct {
		name    string
		step    core.Step
		wantErr bool
	}{
		{
			name:    "When_config_is_invalid_then_should_return_error",
			step:    core.Step{ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"cpu_cores": "abc"}}},
			wantErr: true,
		},
		{
			name: "When_config_is_valid_then_should_create_executor",
			step: core.Step{ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"name": "my-sandbox"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newTenki(context.Background(), tt.step)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestTenkiExecutor_SetStdoutAndStderr(t *testing.T) {
	e := &tenkiExecutor{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	e.SetStdout(out)
	e.SetStderr(errOut)

	assert.Same(t, out, e.stdout)
	assert.Same(t, errOut, e.stderr)
}

func TestTenkiExecutor_ExitCode(t *testing.T) {
	e := &tenkiExecutor{}
	e.setExitCode(42)
	assert.Equal(t, 42, e.ExitCode())
}

func TestTenkiExecutor_Run(t *testing.T) {
	tests := []struct {
		name    string
		step    core.Step
		wantErr bool
	}{
		{
			name: "When_no_command_or_script_then_should_return_nil",
			step: core.Step{},
		},
		{
			name:    "When_client_creation_fails_then_should_return_error",
			step:    core.Step{Commands: []core.CommandEntry{{CmdWithArgs: "echo hi"}}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear the env token so client creation fails locally without
			// reaching the API.
			t.Setenv("TENKI_API_KEY", "")
			t.Setenv("TENKI_AUTH_TOKEN", "")

			e := &tenkiExecutor{step: tt.step, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
			err := e.Run(context.Background())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTenkiExecutor_CreateOptions(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		want int
	}{
		{
			name: "When_config_is_minimal_then_should_only_set_wait_ready",
			cfg:  config{},
			want: 1,
		},
		{
			name: "When_all_fields_are_set_then_should_add_one_option_each",
			cfg: config{
				Name:        "my-sandbox",
				Image:       "ubuntu",
				CPUCores:    2,
				MemoryMB:    4096,
				MaxDuration: time.Hour,
				Env:         []string{"A=1"},
				ProjectID:   "proj-1",
				WorkspaceID: "ws-1",
			},
			want: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env fallbacks so the option count depends only on cfg.
			t.Setenv("TENKI_PROJECT_ID", "")
			t.Setenv("TENKI_WORKSPACE_ID", "")

			e := &tenkiExecutor{cfg: tt.cfg}
			assert.Len(t, e.createOptions(), tt.want)
		})
	}
}

func TestTenkiExecutor_Kill(t *testing.T) {
	tests := []struct {
		name       string
		closed     bool
		wantCancel bool
	}{
		{
			name:       "When_already_closed_then_should_be_noop",
			closed:     true,
			wantCancel: false,
		},
		{
			name:       "When_not_closed_and_no_owned_session_then_should_cancel",
			closed:     false,
			wantCancel: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cancelled := false
			e := &tenkiExecutor{closed: tt.closed, cancel: func() { cancelled = true }}
			require.NoError(t, e.Kill(nil))
			assert.Equal(t, tt.wantCancel, cancelled)
		})
	}
}

func TestTenkiExecutor_NewClient(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config
		wantErr bool
	}{
		{
			name: "When_api_key_is_set_then_should_create_client",
			cfg:  config{APIKey: "tk_test"},
		},
		{
			name: "When_api_url_is_set_then_should_create_client",
			cfg:  config{APIKey: "tk_test", APIURL: "https://api.example.com"},
		},
		{
			name:    "When_auth_token_is_missing_then_should_return_error",
			cfg:     config{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Isolate from any TENKI_* token in the environment so the missing
			// token case is deterministic.
			t.Setenv("TENKI_API_KEY", "")
			t.Setenv("TENKI_AUTH_TOKEN", "")

			e := &tenkiExecutor{cfg: tt.cfg}
			got, err := e.newClient()
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

func TestTenkiExecutor_ResolveShell(t *testing.T) {
	tests := []struct {
		name      string
		cfg       config
		step      core.Step
		wantShell string
		wantArgs  []string
	}{
		{
			name:      "When_config_shell_is_set_then_should_use_config_shell_and_args",
			cfg:       config{Shell: "/bin/bash", ShellArgs: []string{"-o", "pipefail"}},
			step:      core.Step{Shell: "/bin/zsh"},
			wantShell: "/bin/bash",
			wantArgs:  []string{"-o", "pipefail"},
		},
		{
			name:      "When_only_step_shell_is_set_then_should_use_step_shell_and_args",
			cfg:       config{},
			step:      core.Step{Shell: "/bin/zsh", ShellArgs: []string{"-e"}},
			wantShell: "/bin/zsh",
			wantArgs:  []string{"-e"},
		},
		{
			name:      "When_no_shell_is_set_then_should_use_default",
			cfg:       config{},
			step:      core.Step{},
			wantShell: defaultShell,
			wantArgs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &tenkiExecutor{step: tt.step, cfg: tt.cfg}
			shell, args := e.resolveShell()
			assert.Equal(t, tt.wantShell, shell)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}

func TestTenkiExecutor_WorkingDir(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		step core.Step
		want string
	}{
		{
			name: "When_config_working_dir_is_set_then_should_use_config_dir",
			cfg:  config{WorkingDir: "/home/tenki/a"},
			step: core.Step{Dir: "/home/tenki/b"},
			want: "/home/tenki/a",
		},
		{
			name: "When_only_step_dir_is_set_then_should_use_step_dir",
			cfg:  config{},
			step: core.Step{Dir: "/home/tenki/b"},
			want: "/home/tenki/b",
		},
		{
			name: "When_no_dir_is_set_then_should_return_empty",
			cfg:  config{},
			step: core.Step{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &tenkiExecutor{step: tt.step, cfg: tt.cfg}
			assert.Equal(t, tt.want, e.workingDir())
		})
	}
}

func TestTenkiExecutor_ExecEnv(t *testing.T) {
	tests := []struct {
		name string
		cfg  config
		step core.Step
		want map[string]string
	}{
		{
			name: "When_session_and_step_env_set_then_step_env_wins",
			cfg:  config{Env: []string{"A=1", "B=2"}},
			step: core.Step{Env: []string{"B=override", "C=3"}},
			want: map[string]string{"A": "1", "B": "override", "C": "3"},
		},
		{
			name: "When_no_env_set_then_should_return_nil",
			cfg:  config{},
			step: core.Step{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &tenkiExecutor{step: tt.step, cfg: tt.cfg}
			assert.Equal(t, tt.want, e.execEnv())
		})
	}
}

func TestTenkiExecutor_BuildScript(t *testing.T) {
	tests := []struct {
		name string
		step core.Step
		want string
	}{
		{
			name: "When_script_is_set_then_should_wrap_script",
			step: core.Step{Script: "echo hi"},
			want: "set -e\necho hi\n",
		},
		{
			name: "When_script_ends_with_newline_then_should_not_add_extra_newline",
			step: core.Step{Script: "echo hi\n"},
			want: "set -e\necho hi\n",
		},
		{
			name: "When_single_command_then_should_render_command",
			step: core.Step{Commands: []core.CommandEntry{{CmdWithArgs: "echo hello"}}},
			want: "set -e\necho hello\n",
		},
		{
			name: "When_multiple_commands_then_should_render_each_line",
			step: core.Step{Commands: []core.CommandEntry{
				{CmdWithArgs: "echo one"},
				{CmdWithArgs: "echo two"},
			}},
			want: "set -e\necho one\necho two\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &tenkiExecutor{step: tt.step}
			assert.Equal(t, tt.want, e.buildScript())
		})
	}
}

func TestCommandString(t *testing.T) {
	tests := []struct {
		name string
		cmd  core.CommandEntry
		want string
	}{
		{
			name: "When_cmd_with_args_is_set_then_should_use_original_string",
			cmd:  core.CommandEntry{Command: "echo", Args: []string{"hi"}, CmdWithArgs: "echo $FOO"},
			want: "echo $FOO",
		},
		{
			name: "When_only_command_then_should_return_command",
			cmd:  core.CommandEntry{Command: "ls"},
			want: "ls",
		},
		{
			name: "When_command_and_args_then_should_quote_args",
			cmd:  core.CommandEntry{Command: "echo", Args: []string{"a b"}},
			want: "echo 'a b'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, commandString(tt.cmd))
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{
			name:   "When_first_value_is_set_then_should_return_first",
			values: []string{"a", "b"},
			want:   "a",
		},
		{
			name:   "When_first_is_empty_then_should_fall_back_to_next",
			values: []string{"", "b"},
			want:   "b",
		},
		{
			name:   "When_first_is_whitespace_then_should_fall_back_to_next",
			values: []string{"   ", "b"},
			want:   "b",
		},
		{
			name:   "When_all_values_are_empty_then_should_return_empty",
			values: []string{"", "  "},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, firstNonEmpty(tt.values...))
		})
	}
}

func TestEnvSliceToMap(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		want    map[string]string
	}{
		{
			name:    "When_entries_are_empty_then_should_return_nil",
			entries: nil,
			want:    nil,
		},
		{
			name:    "When_entries_are_valid_then_should_map_pairs",
			entries: []string{"A=1", "B=2"},
			want:    map[string]string{"A": "1", "B": "2"},
		},
		{
			name:    "When_some_entries_are_malformed_then_should_skip_them",
			entries: []string{"NOEQUAL", "=novalue", "C=3"},
			want:    map[string]string{"C": "3"},
		},
		{
			name:    "When_all_entries_are_malformed_then_should_return_nil",
			entries: []string{"NOEQUAL", "=novalue"},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, envSliceToMap(tt.entries))
		})
	}
}

func TestCleanupTimeoutOf(t *testing.T) {
	tests := []struct {
		name string
		env  runtime.Env
		want time.Duration
	}{
		{
			name: "When_dag_sets_max_cleanup_time_then_should_return_it",
			env:  runtime.Env{Context: exec.Context{DAG: &core.DAG{MaxCleanUpTime: 5 * time.Second}}},
			want: 5 * time.Second,
		},
		{
			name: "When_dag_max_cleanup_time_is_zero_then_should_return_zero",
			env:  runtime.Env{Context: exec.Context{DAG: &core.DAG{}}},
			want: 0,
		},
		{
			name: "When_dag_is_nil_then_should_return_zero",
			env:  runtime.Env{},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, cleanupTimeoutOf(tt.env))
		})
	}
}

func TestWithCleanupTimeout(t *testing.T) {
	t.Run("When_timeout_is_positive_then_should_bound_the_context", func(t *testing.T) {
		ctx, cancel := withCleanupTimeout(context.Background(), time.Second)
		defer cancel()
		_, ok := ctx.Deadline()
		assert.True(t, ok)
	})

	t.Run("When_timeout_is_zero_then_should_leave_the_context_unbounded", func(t *testing.T) {
		base := context.Background()
		ctx, cancel := withCleanupTimeout(base, 0)
		defer cancel()
		assert.Equal(t, base, ctx)
		_, ok := ctx.Deadline()
		assert.False(t, ok)
	})
}

func TestHasShellConfigured(t *testing.T) {
	tests := []struct {
		name string
		step core.Step
		want bool
	}{
		{
			name: "When_config_shell_is_set_then_should_return_true",
			step: core.Step{ExecutorConfig: core.ExecutorConfig{Config: map[string]any{"shell": "/bin/bash"}}},
			want: true,
		},
		{
			name: "When_only_step_shell_is_set_then_should_return_true",
			step: core.Step{Shell: "/bin/bash"},
			want: true,
		},
		{
			name: "When_no_shell_is_set_then_should_return_false",
			step: core.Step{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasShellConfigured(tt.step))
		})
	}
}
