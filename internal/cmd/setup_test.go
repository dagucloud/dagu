// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogDir(t *testing.T) {
	tests := []struct {
		name     string
		config   cmd.LogConfig
		wantErr  bool
		validate func(t *testing.T, path string)
	}{
		{
			name: "UsingLogDir",
			config: cmd.LogConfig{
				BaseDir: t.TempDir(),
				Name:    "test_dag",
			},
			validate: func(t *testing.T, path string) {
				assert.Contains(t, path, "test_dag")
				assert.DirExists(t, path)
			},
		},
		{
			name: "UsingDAGLogDir",
			config: cmd.LogConfig{
				DAGLogDir: filepath.Join(t.TempDir(), "custom"),
				Name:      "test_dag",
			},
			validate: func(t *testing.T, path string) {
				assert.Contains(t, path, "custom")
				assert.Contains(t, path, "test_dag")
				assert.DirExists(t, path)
			},
		},
		{
			name: "WithSpecialCharactersInDAGName",
			config: cmd.LogConfig{
				BaseDir: t.TempDir(),
				Name:    "test/dag*special",
			},
			validate: func(t *testing.T, path string) {
				assert.NotContains(t, path, "/dag*")
				assert.DirExists(t, path)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logDir, err := tt.config.LogDir()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			tt.validate(t, logDir)
		})
	}
}

func TestLogFileName(t *testing.T) {
	t.Run("FilenameFormat", func(t *testing.T) {
		cfg := cmd.LogConfig{
			Name:     "test dag",
			DAGRunID: "12345678901234", // Longer than 8 chars to test truncation
		}

		filename := cfg.LogFile()

		assert.Contains(t, filename, "dag-run")
		assert.Contains(t, filename, time.Now().Format("20060102"))
		assert.Contains(t, filename, "12345678")  // Should be truncated
		assert.NotContains(t, filename, "901234") // Shouldn't contain the rest
		assert.Contains(t, filename, ".log")

		// Test proper sanitization
		assert.NotContains(t, filename, " ") // Space should be replaced
	})
}

func TestLogConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  cmd.LogConfig
		wantErr bool
	}{
		{
			name: "ValidSettings",
			config: cmd.LogConfig{
				BaseDir: "/tmp",
				Name:    "test",
			},
			wantErr: false,
		},
		{
			name: "EmptyDAGName",
			config: cmd.LogConfig{
				BaseDir: "/tmp",
			},
			wantErr: true,
		},
		{
			name: "NoDirectories",
			config: cmd.LogConfig{
				Name: "test",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
