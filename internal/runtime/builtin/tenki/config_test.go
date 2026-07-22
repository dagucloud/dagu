// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tenki

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		mapCfg  map[string]any
		want    config
		wantErr bool
	}{
		{
			name:    "When_field_type_is_invalid_then_should_return_error",
			mapCfg:  map[string]any{"cpu_cores": "abc"},
			wantErr: true,
		},
		{
			name:    "When_create_timeout_is_invalid_then_should_return_error",
			mapCfg:  map[string]any{"create_timeout": "not-a-duration"},
			wantErr: true,
		},
		{
			name:    "When_max_duration_is_invalid_then_should_return_error",
			mapCfg:  map[string]any{"max_duration": "not-a-duration"},
			wantErr: true,
		},
		{
			name:   "When_config_is_empty_then_should_apply_defaults",
			mapCfg: nil,
			want:   config{CreateTimeout: defaultCreateTimeout, MaxDuration: defaultMaxDuration},
		},
		{
			name: "When_config_is_fully_specified_then_should_decode_successfully",
			mapCfg: map[string]any{
				"name":           "my-sandbox",
				"image":          "ubuntu",
				"cpu_cores":      4,
				"memory_mb":      8192,
				"session_id":     "sess-123",
				"keep_session":   true,
				"create_timeout": "90s",
				"max_duration":   "1h",
				"env":            []string{"FOO=bar"},
				"working_dir":    "/home/tenki/work",
				"shell":          "/bin/bash",
				"shell_args":     []string{"-o", "pipefail"},
				"api_key":        "tk_test",
				"api_url":        "https://api.example.com",
				"project_id":     "proj-123",
				"workspace_id":   "ws-123",
			},
			want: config{
				Name:          "my-sandbox",
				Image:         "ubuntu",
				CPUCores:      4,
				MemoryMB:      8192,
				SessionID:     "sess-123",
				KeepSession:   true,
				CreateTimeout: 90 * time.Second,
				MaxDuration:   time.Hour,
				Env:           []string{"FOO=bar"},
				WorkingDir:    "/home/tenki/work",
				Shell:         "/bin/bash",
				ShellArgs:     []string{"-o", "pipefail"},
				APIKey:        "tk_test",
				APIURL:        "https://api.example.com",
				ProjectID:     "proj-123",
				WorkspaceID:   "ws-123",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadConfig(tt.mapCfg)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		def     time.Duration
		want    time.Duration
		wantErr bool
	}{
		{
			name:  "When_string_is_empty_then_should_return_default",
			input: "",
			def:   5 * time.Second,
			want:  5 * time.Second,
		},
		{
			name:    "When_string_is_invalid_then_should_return_error",
			input:   "not-a-duration",
			def:     5 * time.Second,
			wantErr: true,
		},
		{
			name:  "When_string_is_valid_then_should_parse_duration",
			input: "90s",
			def:   5 * time.Second,
			want:  90 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration("field", tt.input, tt.def)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseShellConfig(t *testing.T) {
	tests := []struct {
		name      string
		shell     string
		args      []string
		wantShell string
		wantArgs  []string
	}{
		{
			name:      "When_shell_is_empty_then_should_return_empty_shell_and_given_args",
			shell:     "",
			args:      []string{"-e"},
			wantShell: "",
			wantArgs:  []string{"-e"},
		},
		{
			name:      "When_shell_has_no_embedded_args_then_should_keep_given_args",
			shell:     "/bin/bash",
			args:      []string{"-e"},
			wantShell: "/bin/bash",
			wantArgs:  []string{"-e"},
		},
		{
			name:      "When_shell_embeds_args_then_should_split_and_append",
			shell:     "/bin/bash -o pipefail",
			args:      []string{"-e"},
			wantShell: "/bin/bash",
			wantArgs:  []string{"-o", "pipefail", "-e"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shell, args, err := parseShellConfig(tt.shell, tt.args)
			require.NoError(t, err)
			assert.Equal(t, tt.wantShell, shell)
			assert.Equal(t, tt.wantArgs, args)
		})
	}
}
